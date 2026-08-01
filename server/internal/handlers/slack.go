package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/models"
)

// Slack's interactivity contract: the request is signed with the app's signing
// secret over the raw body, and anything older than five minutes is refused as
// a replay. See https://api.slack.com/authentication/verifying-requests-from-slack
const (
	slackSignatureVersion = "v0"
	slackMaxRequestAge    = 5 * time.Minute
	// Slack truncates a request body it considers oversized, and an unbounded
	// read here would be a free memory sink on an unauthenticated endpoint.
	slackMaxBodyBytes = 1 << 20
)

// slackInteractionPayload is the subset of Slack's block_actions payload this
// endpoint uses.
type slackInteractionPayload struct {
	Type string `json:"type"`
	User struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Name     string `json:"name"`
	} `json:"user"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	} `json:"actions"`
}

// PostSlackClassifiedAction handles Approve/Reject clicks on the Slack
// notification posted when a classified is submitted. It is unauthenticated in
// the CMS's own terms — Slack has no session — so the signature check is the
// entire authorization story and must run before anything else touches the body.
//
// @Summary Slack interactivity callback for classified moderation
// @Tags classifieds
// @Accept x-www-form-urlencoded
// @Produce json
// @Success 200 {object} map[string]any
// @Failure 400 {object} models.ErrorResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 503 {object} models.ErrorResponse
// @Router /v1/integrations/slack/classifieds [post]
func PostSlackClassifiedAction(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := strings.TrimSpace(os.Getenv("SLACK_SIGNING_SECRET"))
		if secret == "" {
			// Without the secret every request is unverifiable, so the endpoint
			// refuses to act rather than trusting the caller.
			logSlackRejection(r, slackRejectionUnconfigured)
			writeError(w, http.StatusServiceUnavailable, "slack integration is not configured")
			return
		}

		rawBody, err := io.ReadAll(io.LimitReader(r.Body, slackMaxBodyBytes))
		if err != nil {
			writeError(w, http.StatusBadRequest, "could not read request body")
			return
		}

		if reason := slackVerificationFailure(secret, r.Header.Get("X-Slack-Request-Timestamp"), string(rawBody), r.Header.Get("X-Slack-Signature"), time.Now()); reason != "" {
			logSlackRejection(r, reason)
			writeError(w, http.StatusUnauthorized, "invalid slack signature")
			return
		}

		form, err := url.ParseQuery(string(rawBody))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid form body")
			return
		}

		var payload slackInteractionPayload
		if err := json.Unmarshal([]byte(form.Get("payload")), &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		if len(payload.Actions) == 0 {
			writeError(w, http.StatusBadRequest, "no action in payload")
			return
		}

		action := payload.Actions[0]
		var status string
		switch strings.ToLower(strings.TrimSpace(action.ActionID)) {
		case "approved", "approve":
			status = models.ClassifiedStatusApproved
		case "rejected", "reject":
			status = models.ClassifiedStatusRejected
		default:
			writeError(w, http.StatusBadRequest, "unknown action")
			return
		}

		// The button value is the classified's row id. Messages posted by the
		// old flow carried a base64 blob instead, and those submissions were
		// never persisted anywhere this server can reach.
		id, err := strconv.ParseInt(strings.TrimSpace(action.Value), 10, 64)
		if err != nil || id <= 0 {
			slackReply(w, ":warning: This message predates the CMS classifieds queue — approve it from the CMS instead.")
			return
		}

		classified, err := db.GetClassified(r.Context(), conn, id)
		if err != nil {
			if err == sql.ErrNoRows {
				slackReply(w, ":warning: That classified no longer exists.")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Clicking a button on a stale message must not silently reverse a
		// decision someone already made in the CMS.
		if classified.Status != models.ClassifiedStatusPending {
			slackReply(w, ":information_source: Already "+classified.Status+" by "+decidedByLabel(classified)+".")
			return
		}

		decidedBy := strings.TrimSpace(payload.User.Username)
		if decidedBy == "" {
			decidedBy = strings.TrimSpace(payload.User.Name)
		}
		if decidedBy == "" {
			decidedBy = payload.User.ID
		}

		if err := db.SetClassifiedStatus(r.Context(), conn, id, status, decidedBy, "slack"); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		activity.LogRequest(r, "classified_moderated", "Classified "+strconv.FormatInt(id, 10)+" "+status+" via Slack", "status", status, "actor", decidedBy)

		emoji := ":white_check_mark:"
		if status == models.ClassifiedStatusRejected {
			emoji = ":x:"
		}
		slackReply(w, emoji+" *"+status+"* by @"+decidedBy+"\n>"+strings.ReplaceAll(classified.Message, "\n", "\n>"))
	})
}

func decidedByLabel(c models.Classified) string {
	if strings.TrimSpace(c.DecidedBy) == "" {
		return "someone else"
	}
	return c.DecidedBy
}

// slackReply replaces the original Slack message with the outcome, so the
// buttons cannot be clicked twice and the channel shows who decided what.
func slackReply(w http.ResponseWriter, text string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"replace_original": true,
		"text":             text,
	})
}

// Why a request was turned away. These are logged rather than returned to the
// caller: the response stays a flat "invalid slack signature" so a prober
// learns nothing about which check it failed.
const (
	slackRejectionUnconfigured = "signing_secret_not_set"
	slackRejectionMissingParts = "missing_signature_headers"
	slackRejectionBadTimestamp = "unparseable_timestamp"
	slackRejectionStale        = "timestamp_outside_replay_window"
	slackRejectionBadDigest    = "signature_mismatch"
)

// verifySlackRequest checks Slack's request signature.
func verifySlackRequest(secret, timestamp, body, signature string, now time.Time) bool {
	return slackVerificationFailure(secret, timestamp, body, signature, now) == ""
}

// slackVerificationFailure returns "" when the request verifies, otherwise the
// reason it did not. It is a pure function so the signing rules can be tested
// without standing up an HTTP server.
func slackVerificationFailure(secret, timestamp, body, signature string, now time.Time) string {
	if secret == "" {
		return slackRejectionUnconfigured
	}
	if timestamp == "" || signature == "" {
		return slackRejectionMissingParts
	}

	ts, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return slackRejectionBadTimestamp
	}
	age := now.Sub(time.Unix(ts, 0))
	if age < 0 {
		age = -age
	}
	if age > slackMaxRequestAge {
		return slackRejectionStale
	}

	mac := hmac.New(sha256.New, []byte(secret))
	io.WriteString(mac, slackSignatureVersion+":"+timestamp+":"+body)
	expected := slackSignatureVersion + "=" + hex.EncodeToString(mac.Sum(nil))

	// Constant-time: a byte-by-byte comparison would leak the expected digest
	// to a caller who can time the response.
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return slackRejectionBadDigest
	}
	return ""
}

// SlackInteractivityConfigured reports whether the signing secret is present,
// i.e. whether the Approve/Reject buttons can work at all. The CMS surfaces
// this so the moderation queue does not promise Slack approvals that would
// silently time out in the channel.
func SlackInteractivityConfigured() bool {
	return strings.TrimSpace(os.Getenv("SLACK_SIGNING_SECRET")) != ""
}

// A rejected request is worth knowing about — this is the one public write
// path that no session guards — but it is also trivially floodable, so the
// warnings are throttled and the ones dropped in between are counted into the
// next line rather than lost.
const slackRejectionLogInterval = time.Minute

var slackRejectionLog struct {
	mu         sync.Mutex
	lastAt     time.Time
	suppressed int
}

func logSlackRejection(r *http.Request, reason string) {
	slackRejectionLog.mu.Lock()
	now := time.Now()
	if !slackRejectionLog.lastAt.IsZero() && now.Sub(slackRejectionLog.lastAt) < slackRejectionLogInterval {
		slackRejectionLog.suppressed++
		slackRejectionLog.mu.Unlock()
		return
	}
	suppressed := slackRejectionLog.suppressed
	slackRejectionLog.suppressed = 0
	slackRejectionLog.lastAt = now
	slackRejectionLog.mu.Unlock()

	slog.Warn("rejected Slack interaction",
		"reason", reason,
		"ip", clientIP(r),
		"suppressed_since_last", suppressed,
	)
}
