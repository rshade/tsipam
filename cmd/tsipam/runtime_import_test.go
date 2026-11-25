package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	runtimeModule = "github.com/rshade/ax-go"
	commandDir    = "cmd/tsipam"
)

// runtimeImportViolation explains why the file at path may not import the CLI
// runtime package imported, or returns "". CONTEXT.md allows the root package
// and mcp in cmd/tsipam, and axtest in its tests.
func runtimeImportViolation(path, imported string) string {
	if imported != runtimeModule && !strings.HasPrefix(imported, runtimeModule+"/") {
		return ""
	}
	if filepath.ToSlash(filepath.Dir(path)) != commandDir {
		return path + " imports " + imported + "; only " + commandDir + " may use the CLI runtime"
	}
	allowed := []string{runtimeModule, runtimeModule + "/mcp"}
	if strings.HasSuffix(path, "_test.go") {
		allowed = append(allowed, runtimeModule+"/axtest")
	}
	if !slices.Contains(allowed, imported) {
		return path + " imports " + imported + ", which CONTEXT.md does not allow there"
	}
	return ""
}

// goFiles lists every Go file in the module, relative to its root.
func goFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(moduleRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".") && entry.Name() != ".." {
			return filepath.SkipDir
		}
		if strings.HasSuffix(path, ".go") {
			relative, relErr := filepath.Rel(moduleRoot, path)
			files = append(files, relative)
			return relErr
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func fileImports(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(moduleRoot, path), nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	imports := make([]string, len(file.Imports))
	for index, spec := range file.Imports {
		if imports[index], err = strconv.Unquote(spec.Path.Value); err != nil {
			t.Fatal(err)
		}
	}
	return imports
}

func TestRuntimeImportsStayInTheCommand(t *testing.T) {
	t.Parallel()
	found := false
	for _, path := range goFiles(t) {
		for _, imported := range fileImports(t, path) {
			found = found || imported == runtimeModule
			if violation := runtimeImportViolation(path, imported); violation != "" {
				t.Error(violation)
			}
		}
	}
	if !found {
		t.Error("no file imports the CLI runtime, so this test checks nothing")
	}
}

func TestRuntimeImportViolation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path, imported string
		allowed        bool
	}{
		{"cmd/tsipam/runtime.go", runtimeModule, true},
		{"cmd/tsipam/mcp.go", runtimeModule + "/mcp", true},
		{"cmd/tsipam/output_test.go", runtimeModule + "/axtest", true},
		{"cmd/tsipam/runtime.go", runtimeModule + "/axtest", false},
		{"cmd/tsipam/runtime.go", runtimeModule + "/contract", false},
		{"internal/ipamsvc/service.go", runtimeModule, false},
		{"internal/ipamsvc/service_test.go", runtimeModule + "/axtest", false},
		{"smoke/harness_test.go", runtimeModule, false},
		{"internal/ipamsvc/service.go", "github.com/rshade/ax-gone", true},
	}
	for _, test := range tests {
		t.Run(test.path+" "+test.imported, func(t *testing.T) {
			t.Parallel()
			violation := runtimeImportViolation(test.path, test.imported)
			if (violation == "") != test.allowed {
				t.Errorf("violation = %q, want allowed = %t", violation, test.allowed)
			}
		})
	}
}
