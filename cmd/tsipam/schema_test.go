package main

import (
	"encoding/json"
	"slices"
	"testing"
)

// TestRuntimeSchemaGoldens pins both runtime catalogs. Read the diff after
// -update: a change here reaches every agent that plans from them.
func TestRuntimeSchemaGoldens(t *testing.T) {
	t.Parallel()
	for golden, args := range map[string][]string{
		"ax_schema.golden.json":  {"__schema"},
		"mcp_schema.golden.json": {"__schema", "--as=mcp"},
	} {
		t.Run(golden, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runWith(t, nil, args...)
			if code != exitOK || stderr != "" || !json.Valid([]byte(stdout)) {
				t.Fatalf("exit = %d, stderr = %q", code, stderr)
			}
			assertGolden(t, golden, []byte(stdout))
		})
	}
}

func TestEveryLeafIsDescribed(t *testing.T) {
	t.Parallel()
	for _, name := range leafNames() {
		command := publishedCommandAt(t, name)
		if len(command.NonDeterministicFields) == 0 || command.Capability.Class == "" {
			t.Errorf("%s is described as %+v", name, command)
		}
	}
}

func TestOriginalCatalogStaysUnwrapped(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runWith(t, nil, "-schema")
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &document); err != nil || code != exitOK || stderr != "" {
		t.Fatalf("exit = %d, %v, stderr = %q", code, err, stderr)
	}
	for _, field := range []string{"exit_codes", "sanitization", "commands", "flags"} {
		if document[field] == nil {
			t.Errorf("the document has no top-level %s", field)
		}
	}
	if string(document["schema_version"]) != `"1"` || document["data"] != nil || document["meta"] != nil {
		t.Errorf("the document is wrapped or renumbered: %s", stdout)
	}
}

// TestCatalogListsRuntimeCommandsAndFlags covers what the runtime added to the
// original catalog.
func TestCatalogListsRuntimeCommandsAndFlags(t *testing.T) {
	t.Parallel()
	_, stdout, _ := runWith(t, nil, "-schema")
	var document struct {
		Flags    []schemaFlag    `json:"flags"`
		Commands []schemaCommand `json:"commands"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatal(err)
	}
	var flags []string
	for _, flag := range document.Flags {
		flags = append(flags, flag.Name)
	}
	want := []string{
		"dry-run",
		"format",
		"idempotency-key",
		"sanitize",
		"schema",
		"strict",
		"verbose",
		"version",
		"yes",
	}
	if slices.Sort(flags); !slices.Equal(flags, want) {
		t.Errorf("flags = %v, want %v", flags, want)
	}
	inputs := map[string][]string{commandSiteIDReserve: {"id", "site"}, commandCheckPolicy: {"policy-file"}}
	seen := map[string]bool{}
	for _, command := range document.Commands {
		seen[command.Name] = true
		for _, flag := range command.Flags {
			if slices.Contains(inputs[command.Name], flag.Name) && flag.Required {
				t.Errorf("%s --%s is marked required", command.Name, flag.Name)
			}
		}
	}
	for _, name := range []string{commandMCPServer, "__schema"} {
		if !seen[name] {
			t.Errorf("the catalog does not list %s", name)
		}
	}
}
