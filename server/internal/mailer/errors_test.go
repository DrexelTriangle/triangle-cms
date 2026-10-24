package mailer

import (
	"errors"
	"fmt"
	"net/textproto"
	"testing"
)

func TestIsPermanent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"smtp 5xx", fmt.Errorf("smtp RCPT TO: %w", &textproto.Error{Code: 550, Msg: "no such user"}), true},
		{"smtp 4xx", fmt.Errorf("smtp RCPT TO: %w", &textproto.Error{Code: 451, Msg: "try later"}), false},
		{"unbuildable message", &PermanentError{Err: errors.New("mailer: message has no HTML body")}, true},
		{"connection refused", errors.New("smtp dial: connection refused"), false},
	}
	for _, c := range cases {
		if got := IsPermanent(c.err); got != c.want {
			t.Errorf("%s: IsPermanent = %v, want %v", c.name, got, c.want)
		}
	}
}
