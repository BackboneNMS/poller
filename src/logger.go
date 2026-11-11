package main

import (
	"log/slog"
	"os"
)

// logTimeFormat is RFC 3339 with milliseconds that always keep their trailing zeros, unlike slog's default which trims
// them, so every log line's timestamp is the same width and the rest of the line stays aligned.
const logTimeFormat = "2006-01-02T15:04:05.000Z07:00"

func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey && attr.Value.Kind() == slog.KindTime {
				return slog.String(slog.TimeKey, attr.Value.Time().Format(logTimeFormat))
			}

			return attr
		},
	}))
}
