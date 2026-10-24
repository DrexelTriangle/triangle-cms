package mailer

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

var (
	testFrom = &mail.Address{Name: "The Triangle", Address: "notify@thetriangle.org"}
	testTo   = &mail.Address{Address: "reader@example.com"}
)

func testComposer() *Newsletter {
	return &Newsletter{From: testFrom, PublicURL: "https://cms.example.org/"}
}

// parse reads a rendered message back into its headers and decoded parts.
func parse(t *testing.T, raw []byte) (*mail.Message, map[string]string) {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q (%v)", msg.Header.Get("Content-Type"), err)
	}
	parts := map[string]string{}
	reader := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextRawPart: %v", err)
		}
		decoded, err := io.ReadAll(quotedprintable.NewReader(part))
		if err != nil {
			t.Fatalf("decode part: %v", err)
		}
		partType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		parts[partType] = string(decoded)
	}
	return msg, parts
}

func TestCompose_PersonalisesLinksAndHeaders(t *testing.T) {
	issue := Issue{
		ID:        7,
		Subject:   "To The Point: café edition",
		EmailHTML: `<a href="{{view_online_url}}">View online</a> <a href="{{unsubscribe_url}}">Unsubscribe</a> <a href="{{manage_subscription_url}}">Manage</a>`,
	}
	m := testComposer().Compose(issue, testTo, "abc123", nil)
	m.Date = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	raw, err := m.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	msg, parts := parse(t, raw)

	const unsubscribe = "https://cms.example.org/v1/newsletter/unsubscribe?t=abc123"
	if got := msg.Header.Get("List-Unsubscribe"); got != "<"+unsubscribe+">" {
		t.Errorf("List-Unsubscribe = %q", got)
	}
	if got := msg.Header.Get("List-Unsubscribe-Post"); got != "List-Unsubscribe=One-Click" {
		t.Errorf("List-Unsubscribe-Post = %q", got)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != issue.Subject {
		t.Errorf("Subject = %q (%v)", subject, err)
	}
	if !strings.HasPrefix(msg.Header.Get("Message-ID"), "<") || !strings.HasSuffix(msg.Header.Get("Message-ID"), "@thetriangle.org>") {
		t.Errorf("Message-ID = %q", msg.Header.Get("Message-ID"))
	}
	html := parts["text/html"]
	if strings.Contains(html, "{{") {
		t.Errorf("placeholder left in HTML: %s", html)
	}
	for _, want := range []string{
		`href="https://cms.example.org/v1/newsletter/issues/7"`,
		`href="` + unsubscribe + `">Unsubscribe`,
		`href="` + unsubscribe + `">Manage`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %s", want)
		}
	}
	if !strings.Contains(parts["text/plain"], unsubscribe) {
		t.Errorf("text part has no unsubscribe link: %q", parts["text/plain"])
	}
}

func TestCompose_TracksLinksButNotUnsubscribe(t *testing.T) {
	issue := Issue{
		ID: 9,
		EmailHTML: `<html><body><a href="https://www.example.org/a?x=1&amp;y=2">A</a> <a href="https://www.example.org/a?x=1&amp;y=2">A again</a>` +
			` <a href="mailto:tips@example.org">Tips</a> <a href="{{unsubscribe_url}}">Unsubscribe</a> <a href="https://untracked.example">U</a></body></html>`,
	}
	urls := TrackableURLs(issue.EmailHTML)
	if len(urls) != 2 || urls[0] != "https://www.example.org/a?x=1&y=2" || urls[1] != "https://untracked.example" {
		t.Fatalf("TrackableURLs = %q", urls)
	}
	m := testComposer().Compose(issue, testTo, "tok", &Tracking{Delivery: "dlv", Links: map[string]int64{urls[0]: 41}})
	click := `href="https://cms.example.org/v1/newsletter/c/dlv/41"`
	if strings.Count(m.HTML, click) != 2 {
		t.Errorf("both copies of the link should redirect: %s", m.HTML)
	}
	for _, kept := range []string{`href="mailto:tips@example.org"`, `href="https://untracked.example"`, `href="https://cms.example.org/v1/newsletter/unsubscribe?t=tok"`} {
		if !strings.Contains(m.HTML, kept) {
			t.Errorf("HTML lost %s", kept)
		}
	}
	if !strings.Contains(m.HTML, `<img src="https://cms.example.org/v1/newsletter/o/dlv" width="1" height="1" alt="" style="display:block;width:1px;height:1px;border:0;"></body>`) {
		t.Errorf("no open pixel before </body>: %s", m.HTML)
	}
}

func TestCompose_MarksTestSends(t *testing.T) {
	m := testComposer().Compose(Issue{ID: 1, Subject: "Hello", EmailHTML: "<p>x</p>"}, testTo, TestToken, nil)
	if m.Subject != "[Test] Hello" {
		t.Errorf("Subject = %q", m.Subject)
	}
}

func TestBytes_HeaderInjection(t *testing.T) {
	m := &Message{From: testFrom, To: testTo, Subject: "Hi\r\nBcc: everyone@example.com", HTML: "<p>x</p>"}
	if _, err := m.Bytes(); err == nil {
		t.Error("Bytes accepted a subject with a line break")
	}

	// A line break in a display name is encoded, not rejected.
	m = &Message{From: testFrom, To: &mail.Address{Name: "Hi\r\nBcc: everyone@example.com", Address: "a@example.com"}, HTML: "<p>x</p>"}
	raw, err := m.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	msg, _ := parse(t, raw)
	if bcc := msg.Header.Get("Bcc"); bcc != "" {
		t.Errorf("display name injected Bcc: %q", bcc)
	}
}

// How fakeSMTP misbehaves, for the error tests.
type smtpFault int

const (
	smtpOK smtpFault = iota
	// Queue the message, then drop the connection before QUIT.
	smtpHangUpAfterQueuing
	// Read the message, then drop the connection without replying to it.
	smtpHangUpBeforeReply
	// Refuse the recipient with a 4xx.
	smtpDeferRecipient
)

// fakeSMTP accepts one message and returns what it was given.
func fakeSMTP(t *testing.T) (addr string, got <-chan [3]string) {
	return fakeSMTPWithFault(t, smtpOK)
}

func fakeSMTPWithFault(t *testing.T, fault smtpFault) (addr string, got <-chan [3]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan [3]string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		tp := textproto.NewConn(conn)
		var from, to string
		_ = tp.PrintfLine("220 fake ESMTP")
		for {
			line, err := tp.ReadLine()
			if err != nil {
				return
			}
			switch cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0]); cmd {
			case "EHLO", "HELO":
				_ = tp.PrintfLine("250 fake")
			case "MAIL":
				from = line
				_ = tp.PrintfLine("250 ok")
			case "RCPT":
				to = line
				if fault == smtpDeferRecipient {
					_ = tp.PrintfLine("450 mailbox busy")
					continue
				}
				_ = tp.PrintfLine("250 ok")
			case "DATA":
				_ = tp.PrintfLine("354 go ahead")
				data, err := io.ReadAll(tp.DotReader())
				if err != nil || fault == smtpHangUpBeforeReply {
					return
				}
				_ = tp.PrintfLine("250 queued")
				ch <- [3]string{from, to, string(data)}
				if fault == smtpHangUpAfterQueuing {
					return
				}
			case "QUIT":
				_ = tp.PrintfLine("221 bye")
				return
			default:
				_ = tp.PrintfLine("502 unknown")
			}
		}
	}()
	return ln.Addr().String(), ch
}

func TestSMTPSender_Delivers(t *testing.T) {
	addr, got := fakeSMTP(t)
	m := testComposer().Compose(Issue{ID: 3, Subject: "Hello", EmailHTML: "<p>{{unsubscribe_url}}</p>"}, testTo, "tok", nil)
	if err := (&SMTPSender{Addr: addr, Timeout: 5 * time.Second}).Send(context.Background(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case env := <-got:
		if env[0] != "MAIL FROM:<notify@thetriangle.org>" || !strings.HasPrefix(env[1], "RCPT TO:<reader@example.com>") {
			t.Errorf("envelope = %q / %q", env[0], env[1])
		}
		msg, err := mail.ReadMessage(bufio.NewReader(strings.NewReader(env[2])))
		if err != nil {
			t.Fatalf("relayed message unreadable: %v", err)
		}
		if msg.Header.Get("List-Unsubscribe") == "" {
			t.Error("relayed message lost List-Unsubscribe")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake SMTP server received nothing")
	}
}

// The worker retries only errors marked transient, so a send whose fate is
// unknown must not be one of them: that would put a second copy in the inbox.
func TestSMTPSender_ClassifiesFailuresForRetry(t *testing.T) {
	m := testComposer().Compose(Issue{ID: 3, Subject: "Hello", EmailHTML: "<p>{{unsubscribe_url}}</p>"}, testTo, "tok", nil)
	send := func(fault smtpFault) error {
		addr, _ := fakeSMTPWithFault(t, fault)
		return (&SMTPSender{Addr: addr, Timeout: 5 * time.Second}).Send(context.Background(), m)
	}

	if err := send(smtpHangUpAfterQueuing); err != nil {
		t.Errorf("a queued message is sent even if QUIT fails, got %v", err)
	}
	if err := send(smtpHangUpBeforeReply); err == nil || !IsPermanent(err) {
		t.Errorf("no reply to the message leaves its fate unknown, so no retry; got %v (permanent=%v)", err, IsPermanent(err))
	}
	if err := send(smtpDeferRecipient); err == nil || IsPermanent(err) {
		t.Errorf("a 4xx on the recipient is worth a retry; got %v (permanent=%v)", err, IsPermanent(err))
	}
}
