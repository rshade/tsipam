package registry

import "errors"

// ErrNotFound is the sentinel for an absent registry. Backends wrap it so
// [errors.Is] matches; the HuJSON backend also matches [os.ErrNotExist] and
// keeps the directory-versus-file message text.
var ErrNotFound = errors.New("registry not found")

// CommitError is a failure while making an Update's mutations durable.
// The ledger adds the operation prefix callers already see.
type CommitError struct {
	Err error
}

func (e *CommitError) Error() string {
	if e == nil || e.Err == nil {
		return "registry commit failed"
	}
	return e.Err.Error()
}

func (e *CommitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// StoredStateError strips an InvalidInputError marker so a stored-key
// failure is not classified as caller input. Backends that canonicalize
// stored keys use it. Other errors are returned unchanged.
func StoredStateError(err error) error {
	if invalid, ok := errors.AsType[*InvalidInputError](err); ok {
		return invalid.Unwrap()
	}
	return err
}
