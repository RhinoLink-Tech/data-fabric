package mcp

import (
	"context"
	"log/slog"
)

// The MCP SDK's transport messages can contain session IDs and protocol
// details. Retain an error signal without sending those values to the logs.
type safeMCPTransportHandler struct {
	sink slog.Handler
}

func (h safeMCPTransportHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelError && h.sink.Enabled(ctx, level)
}

func (h safeMCPTransportHandler) Handle(ctx context.Context, record slog.Record) error {
	safe := slog.NewRecord(record.Time, record.Level, "MCP transport error", record.PC)
	return h.sink.Handle(ctx, safe)
}

func (h safeMCPTransportHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h safeMCPTransportHandler) WithGroup(string) slog.Handler      { return h }

func safeMCPTransportLogger() *slog.Logger {
	return slog.New(safeMCPTransportHandler{sink: slog.Default().Handler()})
}
