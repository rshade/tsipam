// Package diagnostics defines the small logging surface used by tail-ipam's
// I/O layers. A *slog.Logger satisfies Logger; keeping the interface here
// prevents collection and registry packages from owning logger construction.
package diagnostics

// Logger is the structured, leveled logging surface used by collectors.
// Messages and attributes must not contain tailnet identities: verbose output
// stays safe when TAILSCALE_SANITIZE is active.
type Logger interface {
	Debug(msg string, args ...any)
	Warn(msg string, args ...any)
}

// Debug emits a structured debug event when logger is configured.
func Debug(logger Logger, msg string, args ...any) {
	if logger != nil {
		logger.Debug(msg, args...)
	}
}

// Warn emits a structured warning event when logger is configured.
func Warn(logger Logger, msg string, args ...any) {
	if logger != nil {
		logger.Warn(msg, args...)
	}
}
