package hujsonstore

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rshade/tsipam/internal/diagnostics"
)

const (
	lockSuffix         = ".lock"
	lockRetryInterval  = 20 * time.Millisecond
	lockAcquireTimeout = 5 * time.Second
)

// withRegistryLock runs fn while holding the exclusive lock file at path+".lock".
// The events match the historical registry lock span.
func withRegistryLock(path string, logger diagnostics.Logger, fn func() error) error {
	lockPath := path + lockSuffix
	deadline := time.Now().Add(lockAcquireTimeout)
	diagnostics.Debug(logger, "acquiring registry lock")
	waiting := false
	for {
		lockFile, openErr := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr == nil {
			if closeErr := lockFile.Close(); closeErr != nil {
				_ = os.Remove(lockPath)
				return fmt.Errorf("acquiring lock: %w", identityFreePathError(closeErr))
			}
			break
		}
		if !errors.Is(openErr, os.ErrExist) {
			return fmt.Errorf("acquiring lock: %w", identityFreePathError(openErr))
		}
		if !waiting {
			diagnostics.Debug(logger, "waiting for registry lock")
			waiting = true
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"acquiring lock: timed out after %s waiting for a concurrent tsipam to finish",
				lockAcquireTimeout,
			)
		}
		time.Sleep(lockRetryInterval)
	}
	diagnostics.Debug(logger, "registry lock acquired", "waited", waiting)
	defer func() {
		if removeErr := os.Remove(lockPath); removeErr != nil {
			diagnostics.Warn(logger, "releasing registry lock failed", "error", identityFreePathError(removeErr))
			return
		}
		diagnostics.Debug(logger, "registry lock released")
	}()
	return fn()
}

// identityFreePathError keeps a filesystem reason while dropping a path that
// would embed the tailnet routing key.
func identityFreePathError(err error) error {
	if pathError, ok := errors.AsType[*os.PathError](err); ok {
		return pathError.Err
	}
	return err
}
