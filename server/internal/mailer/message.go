// Package mailer builds newsletter emails and hands them to a Sender.
//
// The message is built here, once, as RFC 5322 bytes, so every Sender delivers
// exactly the same thing: the SMTP sender writes the bytes to a relay (Mailpit
// in the laptop sandbox), and the Gmail API sender will upload them as the raw
// message.
package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

// Sender delivers one message to its recipients.
type Sender interface {
	Send(ctx context.Context, m *Message) error
}

// Message is one email to one recipient. Newsletter mail is personalised (each
// copy carries its own unsubscribe link), so there is never more than one To.
type Message struct {
	From    *mail.Address
	To      *mail.Address
	Subject string
	HTML    string
	Text    string
	// UnsubscribeURL, when set, becomes the List-Unsubscribe header with RFC
	// 8058 one-click, which Gmail and Yahoo require of bulk senders. It must be
	// an https URL that unsubscribes on a POST with no further confirmation.
	UnsubscribeURL string
	// Date and MessageID default to now and a random id; tests pin them.
	Date      time.Time
	MessageID string
}

// Bytes renders the message as multipart/alternative with a plain-text part
// first, because clients show the last part they can render.
func (m *Message) Bytes() ([]byte, error) {
	if m.From == nil || m.To == nil {
		return nil, errors.New("mailer: message needs a From and a To")
	}
	if m.HTML == "" {
		return nil, errors.New("mailer: message has no HTML body")
	}
	// From and To need no check: mail.Address.String encodes a name holding
	// control characters as an RFC 2047 word.
	for _, v := range []string{m.Subject, m.UnsubscribeURL, m.MessageID} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, errors.New("mailer: header value contains a line break")
		}
	}

	date := m.Date
	if date.IsZero() {
		date = time.Now()
	}
	messageID := m.MessageID
	if messageID == "" {
		var err error
		if messageID, err = newMessageID(m.From.Address); err != nil {
			return nil, err
		}
	}

	var body bytes.Buffer
	parts := multipart.NewWriter(&body)

	var head bytes.Buffer
	writeHeader := func(name, value string) {
		fmt.Fprintf(&head, "%s: %s\r\n", name, value)
	}
	writeHeader("From", m.From.String())
	writeHeader("To", m.To.String())
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	writeHeader("Date", date.Format(time.RFC1123Z))
	writeHeader("Message-ID", messageID)
	writeHeader("MIME-Version", "1.0")
	if m.UnsubscribeURL != "" {
		writeHeader("List-Unsubscribe", "<"+m.UnsubscribeURL+">")
		writeHeader("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
	}
	writeHeader("Content-Type", `multipart/alternative; boundary="`+parts.Boundary()+`"`)
	head.WriteString("\r\n")

	if err := writePart(parts, "text/plain", m.Text); err != nil {
		return nil, err
	}
	if err := writePart(parts, "text/html", m.HTML); err != nil {
		return nil, err
	}
	if err := parts.Close(); err != nil {
		return nil, err
	}
	head.Write(body.Bytes())
	return head.Bytes(), nil
}

func writePart(parts *multipart.Writer, contentType, content string) error {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType+"; charset=utf-8")
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	w, err := parts.CreatePart(header)
	if err != nil {
		return err
	}
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(content)); err != nil {
		return err
	}
	return qp.Close()
}

func newMessageID(from string) (string, error) {
	domain := "localhost"
	if at := strings.LastIndexByte(from, '@'); at >= 0 && at < len(from)-1 {
		domain = from[at+1:]
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "<" + hex.EncodeToString(b[:]) + "@" + domain + ">", nil
}
