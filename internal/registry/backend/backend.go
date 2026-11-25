// Package backend selects and opens a registry store from a registry URL.
package backend

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
	"github.com/rshade/tsipam/internal/registry/sqlitestore"
)

const (
	schemeFile   = "file"
	schemeSQLite = "sqlite"
)

// errInvalidURL does not echo the URL: it may hold another system's
// credentials.
var errInvalidURL = errors.New("registry URL must be file://<directory> or sqlite://<path>")

// Selector names one registry storage location.
type Selector struct {
	scheme string
	path   string
}

// File selects the directory dir of HuJSON registry files.
func File(dir string) Selector {
	return Selector{scheme: schemeFile, path: dir}
}

// Parse reads a registry URL. Everything after "://" is the path, verbatim: a
// URL parser would read "./x" as a host and mangle a Windows path.
func Parse(raw string) (Selector, error) {
	scheme, path, found := strings.Cut(raw, "://")
	if !found || path == "" || (scheme != schemeFile && scheme != schemeSQLite) {
		return Selector{}, errInvalidURL
	}
	return Selector{scheme: scheme, path: path}, nil
}

// Open returns the selected store. It does not touch storage. A store that
// holds resources also implements [io.Closer].
func Open(selector Selector, logger diagnostics.Logger) (registry.Store, error) {
	switch selector.scheme {
	case schemeFile:
		return hujsonstore.New(selector.path, logger), nil
	case schemeSQLite:
		return sqlitestore.Open(selector.path, logger)
	default:
		return nil, fmt.Errorf("unknown registry scheme %q", selector.scheme)
	}
}

// Locations returns the filesystem names that identify the selected registry
// of tailnet, for a sanitizer to redact.
func (s Selector) Locations(tailnet string) []string {
	if s.scheme == schemeSQLite {
		return []string{s.path, filepath.Base(s.path)}
	}
	return []string{s.path, hujsonstore.Path(s.path, tailnet), hujsonstore.FileName(tailnet)}
}
