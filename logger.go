package main

import (
	"io"
	"log/slog"
	"strings"
)

// Logger is a thin wrapper over slog providing leveled, structured, timestamped
// logs. Embedding *slog.Logger exposes Debug/Info/Warn/Error directly.
type Logger struct {
	*slog.Logger
}

func NewLogger(level slog.Level, format string, out io.Writer) Logger {
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.EqualFold(format, "json") {
		handler = slog.NewJSONHandler(out, opts)
	} else {
		handler = slog.NewTextHandler(out, opts)
	}
	return Logger{slog.New(handler)}
}

// NopLogger discards everything; used in tests.
func NopLogger() Logger {
	return NewLogger(slog.LevelError+1, "text", io.Discard)
}

// With returns a child logger that tags every record with the given fields.
func (l Logger) With(args ...any) Logger {
	return Logger{l.Logger.With(args...)}
}

func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
