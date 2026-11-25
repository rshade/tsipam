package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	modulePath = "github.com/rshade/tsipam"
	moduleRoot = "../.."

	roleShared = "shared"
	roleClient = "client"
	roleServer = "server"
)

// packageRoles places every package under internal/ and gen/ on one side of
// the API. Shared packages serve both sides and import neither.
func packageRoles() map[string]string {
	return map[string]string{
		"gen/ipam":             roleShared,
		"internal/diagnostics": roleShared,
		"internal/registry":    roleShared,

		"gen/http/cli/tsipam":  roleClient,
		"gen/http/ipam/client": roleClient,
		"internal/ipamconn":    roleClient,
		"internal/loopback":    roleClient,

		"gen/http/ipam/server":          roleServer,
		"internal/devices":              roleServer,
		"internal/ipamhttp":             roleServer,
		"internal/ipamsvc":              roleServer,
		"internal/planner":              roleServer,
		"internal/policycheck":          roleServer,
		"internal/registry/hujsonstore": roleServer,
		"internal/registry/storetest":   roleServer,
	}
}

// commandExceptions lists what a command file may import beyond the shared
// packages and the generated client. Only target.go assembles a service.
func commandExceptions() map[string][]string {
	return map[string][]string{
		"target.go": {
			"internal/devices",
			"internal/ipamhttp",
			"internal/ipamsvc",
			"internal/loopback",
			"internal/registry/hujsonstore",
		},
		// devices list reads the Tailscale API from the caller's machine.
		"devices.go": {"internal/devices"},
		// The sanitizer reads identities from the registry file.
		"main.go": {"internal/registry/hujsonstore"},
	}
}

func mayImport(role, imported string) bool {
	importedRole := packageRoles()[imported]
	switch role {
	case roleClient:
		return importedRole == roleClient || imported == "gen/ipam" || imported == "internal/diagnostics"
	case roleServer:
		return importedRole == roleServer || importedRole == roleShared
	default:
		return importedRole == roleShared
	}
}

func isPackageFile(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

// packageDirs returns the module-relative directories under root that hold
// non-test Go files.
func packageDirs(t *testing.T, root string) []string {
	t.Helper()
	found := make(map[string]bool)
	err := filepath.WalkDir(filepath.Join(moduleRoot, root), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !isPackageFile(entry.Name()) {
			return err
		}
		dir, relErr := filepath.Rel(moduleRoot, filepath.Dir(path))
		found[filepath.ToSlash(dir)] = true
		return relErr
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return slices.Sorted(maps.Keys(found))
}

// moduleImports returns the module packages each non-test file in dir imports.
func moduleImports(t *testing.T, dir string) map[string][]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(moduleRoot, dir, "*.go"))
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}
	imports := make(map[string][]string)
	for _, path := range paths {
		name := filepath.Base(path)
		if !isPackageFile(name) {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", path, parseErr)
		}
		imports[name] = nil
		for _, spec := range file.Imports {
			imported, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("reading an import in %s: %v", path, unquoteErr)
			}
			if imported == modulePath {
				imports[name] = append(imports[name], ".")
			} else if local, ok := strings.CutPrefix(imported, modulePath+"/"); ok {
				imports[name] = append(imports[name], local)
			}
		}
	}
	return imports
}

func TestCommandsReachServerCodeOnlyThroughTarget(t *testing.T) {
	t.Parallel()
	exceptions := commandExceptions()
	imports := moduleImports(t, "cmd/tsipam")
	for name, imported := range imports {
		for _, path := range imported {
			allowed := path == "gen/http/ipam/client" || packageRoles()[path] == roleShared
			if !allowed && !slices.Contains(exceptions[name], path) {
				t.Errorf("cmd/tsipam/%s imports %s; go through target.go or list the exception", name, path)
			}
		}
	}
	for name, listed := range exceptions {
		for _, path := range listed {
			if !slices.Contains(imports[name], path) {
				t.Errorf("cmd/tsipam/%s no longer imports %s; remove it from commandExceptions", name, path)
			}
		}
	}
}

func TestClientAndServerPackagesStaySeparate(t *testing.T) {
	t.Parallel()
	roles := packageRoles()
	dirs := slices.Concat(packageDirs(t, "internal"), packageDirs(t, "gen"))
	for _, dir := range dirs {
		role, ok := roles[dir]
		if !ok {
			t.Errorf("%s has no role; add it to packageRoles", dir)
			continue
		}
		for name, imported := range moduleImports(t, dir) {
			for _, path := range imported {
				if !mayImport(role, path) {
					t.Errorf("%s/%s (%s) imports %s", dir, name, role, path)
				}
			}
		}
	}
	for dir := range roles {
		if !slices.Contains(dirs, dir) {
			t.Errorf("%s has a role but no Go files; remove it from packageRoles", dir)
		}
	}
}
