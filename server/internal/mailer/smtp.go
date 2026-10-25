package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"time"
)

// SMTPSender relays through an unauthenticated SMTP server. It exists for the
// laptop sandbox, where Addr is Mailpit: Mailpit keeps every message for
// inspection and delivers none, so a sandbox send can never reach a real
// subscriber. Production sends through GmailSender instead.
type SMTPSender struct {
	Addr string
	// Timeout bounds one whole send when ctx has no earlier deadline.
	Timeout time.Duration
}

func (s *SMTPSender) Send(ctx context.Context, m *Message) error {
	raw, err := m.Bytes()
	if err != nil {
		return &PermanentError{Err: err}
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", s.Addr, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return err
		}
	}

	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if err := c.Mail(m.From.Address); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := c.Rcpt(m.To.Address); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		// A reply refusing the message means it was not taken. Anything else
		// (the connection dropping) leaves it unknown whether the server
		// queued it, and a duplicate is worse than a gap: don't retry.
		var reply *textproto.Error
		if !errors.As(err, &reply) {
			err = &PermanentError{Err: err}
		}
		return fmt.Errorf("smtp end of data: %w", err)
	}
	// The message is accepted; QUIT is only manners, and failing it must not
	// read as a failed send.
	_ = c.Quit()
	return nil
}
