package devices

import (
	"context"
	"errors"
	"net"
	"net/http"
)

// CredentialsError reports that the environment lacks what a live read needs.
// Its text names variables, never their values.
type CredentialsError struct {
	message string
}

func (e *CredentialsError) Error() string { return e.message }

// CollectionError reports that a live read failed.
type CollectionError struct {
	Err error
	// Timeout is true when the read ran out of time.
	Timeout bool
	// Temporary is true when the API answered with an HTTP 5xx status.
	Temporary bool
}

func (e *CollectionError) Error() string { return e.Err.Error() }

func (e *CollectionError) Unwrap() error { return e.Err }

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	netErr, ok := errors.AsType[net.Error](err)
	return ok && netErr.Timeout()
}

func isServerError(status int) bool {
	return status >= http.StatusInternalServerError
}
