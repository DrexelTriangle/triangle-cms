package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultGmailTokenURL = "https://oauth2.googleapis.com/token"
	// The media upload form of users.messages.send takes the RFC 5322 bytes
	// as they are (up to 35 MB), with no base64 "raw" field to build.
	defaultGmailSendURL = "https://gmail.googleapis.com/upload/gmail/v1/users/me/messages/send?uploadType=media"
)

// GmailSender sends through the Gmail API as the mailbox the refresh token
// belongs to (notify@thetriangle.org, the same token WordPress's WP Mail SMTP
// used). It needs no DNS changes, which a domain-verified ESP would, and it
// goes out over HTTPS, so no SMTP port has to be open.
//
// Gmail rewrites a From that isn't the mailbox or one of its verified aliases,
// so NEWSLETTER_FROM should be one of those.
type GmailSender struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	// Client defaults to a client with a 30s timeout; tests point TokenURL
	// and SendURL at a fake.
	Client   *http.Client
	TokenURL string
	SendURL  string

	mu      sync.Mutex
	token   string
	expires time.Time
}

// gmailError is a refused Gmail API call.
type gmailError struct {
	Status int
	Reason string
	Body   string
}

func (e *gmailError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("gmail send: %d %s: %s", e.Status, e.Reason, e.Body)
	}
	return fmt.Sprintf("gmail send: %d: %s", e.Status, e.Body)
}

func (s *GmailSender) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (s *GmailSender) Send(ctx context.Context, m *Message) error {
	raw, err := m.Bytes()
	if err != nil {
		return &PermanentError{Err: err}
	}
	token, err := s.accessToken(ctx)
	if err != nil {
		// Nothing went to Gmail: worth another try.
		return err
	}

	sendURL := s.SendURL
	if sendURL == "" {
		sendURL = defaultGmailSendURL
	}
	// Once the whole request is written, a dropped connection leaves it
	// unknown whether Gmail took the message; see the SMTP sender.
	var written atomic.Bool
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		written.Store(info.Err == nil)
	}}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodPost, sendURL, bytes.NewReader(raw))
	if err != nil {
		return &PermanentError{Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "message/rfc822")

	resp, err := s.client().Do(req)
	if err != nil {
		err = fmt.Errorf("gmail send: %w", err)
		if written.Load() {
			return &PermanentError{Err: err}
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	gerr := &gmailError{Status: resp.StatusCode, Reason: gmailReason(body), Body: strings.TrimSpace(string(body))}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		// The access token went stale early (or was revoked): fetch a new
		// one next time.
		s.mu.Lock()
		s.token = ""
		s.mu.Unlock()
		return gerr
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusForbidden:
		// Rate and daily-quota limits, or the mailbox lost permission: Gmail
		// sent nothing.
		return gerr
	case resp.StatusCode >= 500:
		// Retried, though Gmail may rarely have sent it anyway: a Gmail
		// outage taken as permanent would fail every remaining copy in
		// seconds, which is far worse than an occasional duplicate.
		return gerr
	case resp.StatusCode == http.StatusBadRequest &&
		(gerr.Reason == "failedPrecondition" || gerr.Reason == "FAILED_PRECONDITION"):
		// "Precondition check failed." comes back now and then for a valid
		// recipient, and the same message goes through when sent again.
		// Gmail sent nothing, so it takes the bounded delivery retries.
		return gerr
	default:
		// 400: a recipient or message Gmail won't take, however often it's
		// sent.
		return &PermanentError{Err: gerr}
	}
}

// gmailReason pulls the first error reason (rateLimitExceeded,
// invalidArgument, ...) out of a Google API error body, for the log.
func gmailReason(body []byte) string {
	var parsed struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
			Status string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return ""
	}
	if len(parsed.Error.Errors) > 0 && parsed.Error.Errors[0].Reason != "" {
		return parsed.Error.Errors[0].Reason
	}
	return parsed.Error.Status
}

// accessToken returns a cached access token, refreshing it a minute before it
// expires. Tokens last an hour, so a whole issue needs only a handful.
func (s *GmailSender) accessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Now().Before(s.expires) {
		return s.token, nil
	}

	tokenURL := s.TokenURL
	if tokenURL == "" {
		tokenURL = defaultGmailTokenURL
	}
	form := url.Values{
		"client_id":     {s.ClientID},
		"client_secret": {s.ClientSecret},
		"refresh_token": {s.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("gmail token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)
	if resp.StatusCode != http.StatusOK || parsed.AccessToken == "" {
		// invalid_grant means the refresh token was revoked or expired; it
		// needs minting again for the mailbox. The body names no secret.
		if parsed.Error != "" {
			return "", fmt.Errorf("gmail token: %d %s", resp.StatusCode, parsed.Error)
		}
		return "", errors.New("gmail token: " + resp.Status)
	}
	ttl := time.Duration(parsed.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	s.token = parsed.AccessToken
	s.expires = time.Now().Add(ttl - time.Minute)
	return s.token, nil
}
