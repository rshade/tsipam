package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/rshade/ax-go"
	"github.com/spf13/cobra"
)

// resultDocument is the JSON success document, as a consumer reads it.
type resultDocument struct {
	Data json.RawMessage `json:"data"`
	Meta map[string]any  `json:"meta"`
}

// decodeDocument decodes the one line a successful JSON run writes.
func decodeDocument(stdout []byte) (resultDocument, error) {
	var document resultDocument
	if bytes.Count(stdout, []byte("\n")) != 1 || !bytes.HasSuffix(stdout, []byte("\n")) {
		return document, errors.New("result is not one line")
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return document, err
	}
	if document.Data == nil || document.Meta == nil {
		return document, errors.New("result lacks data or meta")
	}
	return document, nil
}

// decodeData decodes the result object of a successful JSON run.
func decodeData(stdout []byte, result any) error {
	document, err := decodeDocument(stdout)
	if err != nil {
		return err
	}
	return json.Unmarshal(document.Data, result)
}

// newTestRoot is the command tree without the runtime around it, for tests that
// inspect the errors commands return. It resolves the output mode from --format
// as the runtime would, with human as the default, and approves releases.
func newTestRoot(state *commandState, deps commandDependencies) *cobra.Command {
	root := newRootCommandWithDependencies(state, deps)
	prepare := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		mode := ax.ModeHuman
		if format, err := cmd.Flags().GetString("format"); err == nil && format == formatJSON {
			mode = ax.ModeJSON
		}
		cmd.SetContext(ax.WithApproval(ax.WithMode(cmd.Context(), mode), true))
		return prepare(cmd, args)
	}
	return root
}

// humanByDefault is getenv with AGENT_MODE set to human, as a script that reads
// text would set it.
func humanByDefault(getenv func(string) string) func(string) string {
	return func(key string) string {
		if key == "AGENT_MODE" {
			return formatHuman
		}
		return getenv(key)
	}
}

// publishedSchema is the part of __schema these tests read.
type publishedSchema struct {
	Command publishedCommand `json:"command"`
}

type publishedCommand struct {
	Use                    string             `json:"use"`
	NonDeterministicFields []string           `json:"non_deterministic_fields"`
	Commands               []publishedCommand `json:"commands"`
	Capability             struct {
		Class string `json:"class"`
	} `json:"capability"`
}

// publishedCommandAt is __schema's entry for the command at path.
func publishedCommandAt(t *testing.T, path string) publishedCommand {
	t.Helper()
	_, stdout, stderr := runWith(t, nil, "__schema")
	var published publishedSchema
	if err := json.Unmarshal([]byte(stdout), &published); err != nil {
		t.Fatalf("__schema: %v: %s", err, stderr)
	}
	command := published.Command
	for name := range strings.FieldsSeq(path) {
		index := slices.IndexFunc(command.Commands, func(child publishedCommand) bool {
			word, _, _ := strings.Cut(child.Use, " ")
			return word == name
		})
		if index < 0 {
			t.Fatalf("__schema does not list %q", path)
		}
		command = command.Commands[index]
	}
	return command
}

// publishedFields is __schema's list of the fields that vary between identical
// runs of the command at path.
func publishedFields(t *testing.T, path string) []string {
	t.Helper()
	return publishedCommandAt(t, path).NonDeterministicFields
}

// marshalOutput renders a result for comparison.
func marshalOutput(t *testing.T, result outputEnvelope) string {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// maskRun replaces the fields __schema says vary between identical runs. Human
// output has none.
func maskRun(t *testing.T, stdout string) string {
	t.Helper()
	document, err := decodeDocument([]byte(stdout))
	if err != nil {
		return stdout
	}
	var data map[string]any
	if err = json.Unmarshal(document.Data, &data); err != nil {
		t.Fatal(err)
	}
	sections := map[string]map[string]any{"data": data, "meta": document.Meta}
	for _, locator := range publishedFields(t, fmt.Sprint(data["command"])) {
		section, field, _ := strings.Cut(locator, ".")
		if value, ok := sections[section][field].(string); ok {
			stdout = strings.Replace(stdout,
				fmt.Sprintf("%q:%q", field, value), fmt.Sprintf("%q:%q", field, "masked"), 1)
		}
	}
	return stdout
}
