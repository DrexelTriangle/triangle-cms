package mailer

import (
	"errors"
	"net/textproto"
)

// PermanentError wraps a failure that must not be retried: the server refused
// the recipient or the message outright, the message could not be built at
// all, or it may already have been delivered. Anything else (a refused
// connection, a timeout before the message went, a 4xx) is worth another try
// later.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// IsPermanent reports whether err says not to retry the send.
func IsPermanent(err error) bool {
	var p *PermanentError
	if errors.As(err, &p) {
		return true
	}
	// An SMTP 5xx reply is a permanent negative completion (RFC 5321 4.2.1).
	var reply *textproto.Error
	return errors.As(err, &reply) && reply.Code >= 500
}
