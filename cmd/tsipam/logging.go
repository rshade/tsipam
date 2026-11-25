package main

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/rshade/ax-go"
	"github.com/spf13/cobra"

	"github.com/rshade/tsipam/internal/diagnostics"
)

// axLogger adapts the runtime's logger to diagnostics.Logger, so internal
// packages do not depend on the runtime. Every line carries the run's trace ID.
type axLogger struct {
	ctx context.Context //nolint:containedctx // the diagnostics methods take no context, and a logger lives for one run
	log ax.Logger
}

var _ diagnostics.Logger = axLogger{}

// newLogger logs warnings, and with verbose set debug events, to cmd's stderr.
func newLogger(cmd *cobra.Command, verbose bool) axLogger {
	level := zerolog.WarnLevel
	if verbose {
		level = zerolog.DebugLevel
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	return axLogger{ctx: ctx, log: ax.NewLogger(
		ctx, ax.WithLoggerWriter(cmd.ErrOrStderr()), ax.WithLoggerLevel(level),
	)}
}

// Debug implements diagnostics.Logger.
func (l axLogger) Debug(msg string, args ...any) { l.log.Debug(l.ctx).Fields(args).Msg(msg) }

// Warn implements diagnostics.Logger.
func (l axLogger) Warn(msg string, args ...any) { l.log.Warn(l.ctx).Fields(args).Msg(msg) }

// flushLogger drains the run's logger before the process exits.
func (s *commandState) flushLogger(ctx context.Context) error {
	logger, ok := s.logger.(axLogger)
	if !ok {
		return nil
	}
	return ax.Flush(ctx, logger.log)
}
