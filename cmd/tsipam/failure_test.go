package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// errorObject is the JSON failure document, as a consumer reads it.
type errorObject struct {
	ErrorCode     string         `json:"error_code"`
	Message       string         `json:"message"`
	TraceID       string         `json:"trace_id"`
	Tool          string         `json:"tool"`
	Version       string         `json:"version"`
	SchemaVersion string         `json:"schema_version"`
	ActionableFix string         `json:"actionable_fix"`
	Context       map[string]any `json:"context"`
	Retryable     *bool          `json:"retryable"`
}

// splitFailure separates the error object from the other stderr lines.
func splitFailure(t *testing.T, stderr string) (*errorObject, []string) {
	t.Helper()
	var found *errorObject
	var other []string
	for line := range strings.Lines(stderr) {
		var object errorObject
		if json.Unmarshal([]byte(line), &object) != nil || object.ErrorCode == "" {
			other = append(other, line)
			continue
		}
		if found != nil {
			t.Fatalf("stderr holds more than one error object: %s", stderr)
		}
		found = &object
	}
	return found, other
}

// decodeFailure returns the one error object a failed command writes.
func decodeFailure(t *testing.T, stderr string) errorObject {
	t.Helper()
	object, _ := splitFailure(t, stderr)
	if object == nil {
		t.Fatalf("stderr holds no error object: %q", stderr)
	}
	if object.Message == "" || object.TraceID == "" || object.Tool != toolName || object.Version == "" ||
		object.SchemaVersion == "" {
		t.Errorf("error object lacks a required field: %s", stderr)
	}
	return *object
}

// failureText is what a person would read on stderr: the other lines, then the
// error object's decoded message and fix. Reading the encoded bytes instead
// would let an escaped identity pass a leak check.
func failureText(t *testing.T, stderr string) string {
	t.Helper()
	object, other := splitFailure(t, stderr)
	if object == nil {
		return stderr
	}
	return strings.Join(other, "") + object.Message + "\n" + object.ActionableFix
}
