package rest

import (
	"errors"
	"fmt"
)

type ErrKind int

// ErrKind values classify why a request failed, so callers can branch on
// network vs. auth vs. other API errors without string-matching messages.
const (
	KindNetwork ErrKind = iota + 1 // transport failure, timeout, 5xx
	KindAuth                       // 401/403: token missing, expired or revoked
	KindAPI                        // any other 4xx
)

// Error is every failure the client returns. ID/Message come from the
// Mattermost AppError body when present.
type Error struct {
	Kind    ErrKind
	Status  int
	ID      string
	Message string
	Err     error
}

func (e *Error) Error() string {
	switch {
	case e.Err != nil:
		return fmt.Sprintf("mattermost: %v", e.Err)
	case e.ID != "":
		return fmt.Sprintf("mattermost: HTTP %d %s: %s", e.Status, e.ID, e.Message)
	default:
		return fmt.Sprintf("mattermost: HTTP %d", e.Status)
	}
}

func (e *Error) Unwrap() error { return e.Err }

func kindOf(err error) ErrKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}

func IsAuth(err error) bool    { return kindOf(err) == KindAuth }
func IsNetwork(err error) bool { return kindOf(err) == KindNetwork }
