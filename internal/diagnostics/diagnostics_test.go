package diagnostics_test

import (
	"testing"

	"github.com/rshade/tsipam/internal/diagnostics"
)

// mockLogger records calls to its Debug and Warn methods.
type mockLogger struct {
	debugCalls []mockCall
	warnCalls  []mockCall
}

type mockCall struct {
	msg  string
	args []any
}

func (m *mockLogger) Debug(msg string, args ...any) {
	m.debugCalls = append(m.debugCalls, mockCall{msg: msg, args: append([]any{}, args...)})
}

func (m *mockLogger) Warn(msg string, args ...any) {
	m.warnCalls = append(m.warnCalls, mockCall{msg: msg, args: append([]any{}, args...)})
}

func TestDebugWithNilLogger(t *testing.T) {
	t.Parallel()
	// Should not panic when logger is nil
	diagnostics.Debug(nil, "test message", "key", "value")
}

func TestWarnWithNilLogger(t *testing.T) {
	t.Parallel()
	// Should not panic when logger is nil
	diagnostics.Warn(nil, "test message", "key", "value")
}

func TestDebugDelegation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		msg      string
		args     []any
		wantMsg  string
		wantArgs []any
	}{
		{
			name:     "message only",
			msg:      "hello",
			args:     nil,
			wantMsg:  "hello",
			wantArgs: []any{},
		},
		{
			name:     "message with single arg pair",
			msg:      "event",
			args:     []any{"user", "alice"},
			wantMsg:  "event",
			wantArgs: []any{"user", "alice"},
		},
		{
			name:     "message with multiple arg pairs",
			msg:      "operation complete",
			args:     []any{"count", 42, "status", "ok"},
			wantMsg:  "operation complete",
			wantArgs: []any{"count", 42, "status", "ok"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			logger := &mockLogger{}
			diagnostics.Debug(logger, tt.msg, tt.args...)

			if len(logger.debugCalls) != 1 {
				t.Fatalf("expected 1 debug call, got %d", len(logger.debugCalls))
			}

			call := logger.debugCalls[0]
			if call.msg != tt.wantMsg {
				t.Errorf("message: got %q, want %q", call.msg, tt.wantMsg)
			}

			if len(call.args) != len(tt.wantArgs) {
				t.Errorf("arg count: got %d, want %d", len(call.args), len(tt.wantArgs))
			}

			for i, arg := range call.args {
				if arg != tt.wantArgs[i] {
					t.Errorf("arg[%d]: got %v, want %v", i, arg, tt.wantArgs[i])
				}
			}
		})
	}
}

func TestWarnDelegation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		msg      string
		args     []any
		wantMsg  string
		wantArgs []any
	}{
		{
			name:     "message only",
			msg:      "warning",
			args:     nil,
			wantMsg:  "warning",
			wantArgs: []any{},
		},
		{
			name:     "message with single arg pair",
			msg:      "deprecated",
			args:     []any{"feature", "old_api"},
			wantMsg:  "deprecated",
			wantArgs: []any{"feature", "old_api"},
		},
		{
			name:     "message with multiple arg pairs",
			msg:      "resource low",
			args:     []any{"resource", "memory", "percent", 85},
			wantMsg:  "resource low",
			wantArgs: []any{"resource", "memory", "percent", 85},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			logger := &mockLogger{}
			diagnostics.Warn(logger, tt.msg, tt.args...)

			if len(logger.warnCalls) != 1 {
				t.Fatalf("expected 1 warn call, got %d", len(logger.warnCalls))
			}

			call := logger.warnCalls[0]
			if call.msg != tt.wantMsg {
				t.Errorf("message: got %q, want %q", call.msg, tt.wantMsg)
			}

			if len(call.args) != len(tt.wantArgs) {
				t.Errorf("arg count: got %d, want %d", len(call.args), len(tt.wantArgs))
			}

			for i, arg := range call.args {
				if arg != tt.wantArgs[i] {
					t.Errorf("arg[%d]: got %v, want %v", i, arg, tt.wantArgs[i])
				}
			}
		})
	}
}

func TestDebugDoesNotCallWarn(t *testing.T) {
	t.Parallel()
	logger := &mockLogger{}
	diagnostics.Debug(logger, "debug message")

	if len(logger.debugCalls) != 1 {
		t.Fatalf("expected 1 debug call, got %d", len(logger.debugCalls))
	}

	if len(logger.warnCalls) != 0 {
		t.Errorf("Debug should not call Warn, but got %d warn calls", len(logger.warnCalls))
	}
}

func TestWarnDoesNotCallDebug(t *testing.T) {
	t.Parallel()
	logger := &mockLogger{}
	diagnostics.Warn(logger, "warn message")

	if len(logger.warnCalls) != 1 {
		t.Fatalf("expected 1 warn call, got %d", len(logger.warnCalls))
	}

	if len(logger.debugCalls) != 0 {
		t.Errorf("Warn should not call Debug, but got %d debug calls", len(logger.debugCalls))
	}
}
