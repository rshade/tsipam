package hujsonstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/rshade/tsacl/pkg/policycore/hujsonedit"

	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/registry"
)

// Store is the HuJSON registry backend rooted at a directory of per-tailnet files.
type Store struct {
	dir    string
	logger diagnostics.Logger

	mu         sync.Mutex
	failCommit bool
}

var _ registry.Store = (*Store)(nil)

// New returns a store whose files live in dir. A nil logger is quiet.
func New(dir string, logger diagnostics.Logger) *Store {
	return &Store{dir: dir, logger: logger}
}

// FileName returns the registry file name for tailnet.
func FileName(tailnet string) string {
	return tailnet + ".ipam.hujson"
}

// Path returns the registry file path for tailnet inside dir.
func Path(dir, tailnet string) string {
	return filepath.Join(dir, FileName(tailnet))
}

// FailNextCommit makes the next Update whose function returns nil fail the
// commit and leave the file unchanged.
func (s *Store) FailNextCommit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCommit = true
}

func (s *Store) takeFail() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	fail := s.failCommit
	s.failCommit = false
	return fail
}

// View reads one tailnet without taking the registry lock.
func (s *Store) View(
	_ context.Context, tailnet string, opts registry.TxOptions, fn func(registry.Snapshot) error,
) error {
	raw, err := os.ReadFile(Path(s.dir, tailnet))
	if err != nil {
		return s.readError(tailnet, err)
	}
	doc, err := loadBytes(raw)
	if err != nil {
		return err
	}
	if err = requireMaps(doc, opts.Require); err != nil {
		return err
	}
	return fn(snapshot{doc: doc})
}

// Update locks the tailnet file, applies fn to a buffered copy, and writes at most once.
func (s *Store) Update(
	ctx context.Context, tailnet string, opts registry.TxOptions, fn func(registry.Tx) error,
) error {
	path := Path(s.dir, tailnet)
	err := withRegistryLock(path, s.logger, func() error {
		return s.updateLocked(ctx, path, tailnet, opts, fn)
	})
	return s.lockLookupError(tailnet, err)
}

// lockLookupError adds the directory-versus-file lookup sentence to a lock
// failure. An error that already matches ErrNotFound keeps its text.
func (s *Store) lockLookupError(tailnet string, err error) error {
	if err == nil || errors.Is(err, registry.ErrNotFound) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) {
		return s.notFound(tailnet, err)
	}
	return err
}

func (s *Store) updateLocked(
	ctx context.Context,
	path, tailnet string,
	opts registry.TxOptions,
	fn func(registry.Tx) error,
) error {
	pending, doc, created, err := loadForUpdate(path, opts.CreateIfMissing)
	if err != nil {
		if !opts.CreateIfMissing && errors.Is(err, os.ErrNotExist) {
			return s.notFound(tailnet, err)
		}
		return err
	}
	if err = requireMaps(doc, opts.Require); err != nil {
		return err
	}
	message := registryLoadedMessage
	if created {
		message = registryCreatedMessage
	}
	logRegistry(s.logger, message, doc)
	tx := &bufferTx{
		doc:      doc,
		path:     path,
		original: pending,
		pending:  pending,
	}
	if err = fn(tx); err != nil {
		return err
	}
	if s.takeFail() {
		return &registry.CommitError{Err: errors.New("commit failed")}
	}
	if bytes.Equal(tx.pending, tx.original) {
		registry.RunAfterSuccess(ctx)
		return nil
	}
	if writeErr := hujsonedit.WriteRaw(path, tx.pending); writeErr != nil {
		commitErr := writeErr
		if tx.repair != nil {
			commitErr = tx.repair.writeError(writeErr)
		}
		return &registry.CommitError{Err: commitErr}
	}
	registry.RunAfterSuccess(ctx)
	return nil
}

func (s *Store) readError(tailnet string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return s.notFound(tailnet, err)
	}
	return identityFreePathError(err)
}

func loadForUpdate(path string, create bool) ([]byte, *document, bool, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		doc, loadErr := loadBytes(raw)
		if loadErr != nil {
			return nil, nil, false, loadErr
		}
		return raw, doc, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, false, identityFreePathError(err)
	}
	if !create {
		return nil, nil, false, err
	}
	return createEmpty(path)
}

func createEmpty(path string) ([]byte, *document, bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return reloadCreated(path)
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("creating registry: %w", identityFreePathError(err))
	}
	_, writeErr := file.WriteString(emptyDocument)
	closeErr := file.Close()
	if writeErr != nil {
		return nil, nil, false, fmt.Errorf("creating registry: %w", identityFreePathError(writeErr))
	}
	if closeErr != nil {
		return nil, nil, false, fmt.Errorf("creating registry: %w", identityFreePathError(closeErr))
	}
	doc, loadErr := loadBytes([]byte(emptyDocument))
	if loadErr != nil {
		return nil, nil, false, loadErr
	}
	return []byte(emptyDocument), doc, true, nil
}

func reloadCreated(path string) ([]byte, *document, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, false, identityFreePathError(err)
	}
	doc, loadErr := loadBytes(raw)
	if loadErr != nil {
		return nil, nil, false, loadErr
	}
	return raw, doc, false, nil
}

func requireMaps(doc *document, kinds []registry.MapKind) error {
	for _, kind := range kinds {
		if err := doc.scopedError(kind); err != nil {
			return err
		}
	}
	return nil
}

func logRegistry(logger diagnostics.Logger, message string, doc *document) {
	counts := doc.counts()
	diagnostics.Debug(logger, message,
		attrSiteIDCount, counts.SiteIDs,
		attrSubnetPoolCount, counts.SubnetPools,
		attrServiceAddressCount, counts.ServiceAddresses,
		attrUDPPortCount, counts.UDPPorts,
	)
}

// notFoundError is an absent registry file. Error text matches ipamsvc's
// historical lookup message, and both registry.ErrNotFound and [os.ErrNotExist]
// match through Unwrap.
type notFoundError struct {
	msg   string
	cause error
}

func (e *notFoundError) Error() string { return e.msg }

func (e *notFoundError) Unwrap() []error {
	out := []error{registry.ErrNotFound, os.ErrNotExist}
	if e.cause != nil {
		out = append(out, e.cause)
	}
	return out
}

func (s *Store) notFound(tailnet string, cause error) error {
	stripped := identityFreePathError(cause)
	text := stripped.Error()
	msg := fmt.Sprintf(
		"no registry for tailnet %q in %q (a reserve command creates it): %s",
		tailnet, s.dir, text,
	)
	if _, statErr := os.Stat(s.dir); errors.Is(statErr, os.ErrNotExist) {
		msg = fmt.Sprintf("registry directory %q does not exist: %s", s.dir, text)
	}
	return &notFoundError{msg: msg, cause: stripped}
}
