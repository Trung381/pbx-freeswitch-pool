// Package logx provides the shared structured logger (log/slog, JSON on
// stdout) used by every CustomPBX package.
//
// The default logger is configured in this package's init() so that package
// level loggers (var logger = logx.Component("x")) created by other packages
// during their own initialisation already use the JSON handler. The level is
// taken from the LOG_LEVEL environment variable (debug|info|warn|error,
// default info).
package logx

import (
	"context"
	"io"
	"log"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"custompbx/logsafe"
)

const (
	// ServiceName is added to every record as "service".
	ServiceName = "custompbx"
	redacted    = "[REDACTED]"
)

var sensitiveKey = regexp.MustCompile(`(?i)(token|pass|password|secret|authorization|authval|nodepw)`)

func init() {
	Setup(os.Stdout, LevelFromEnv())
}

// ParseLevel converts debug|info|warn|warning|error into a slog.Level.
// Unknown or empty values yield info.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
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

// LevelFromEnv reads LOG_LEVEL.
func LevelFromEnv() slog.Level {
	return ParseLevel(os.Getenv("LOG_LEVEL"))
}

// NewHandler builds the JSON handler: lower-case level, redaction of
// sensitive keys/values and the service attribute.
func NewHandler(w io.Writer, level slog.Level) slog.Handler {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	})
	return h.WithAttrs([]slog.Attr{slog.String("service", ServiceName)})
}

// Setup installs the JSON logger as slog default and routes the standard
// library "log" package (third party libraries) through it.
func Setup(w io.Writer, level slog.Level) *slog.Logger {
	h := NewHandler(w, level)
	l := slog.New(h)
	slog.SetDefault(l)
	std := slog.NewLogLogger(h.WithAttrs([]slog.Attr{slog.String("component", "stdlog")}), slog.LevelInfo)
	log.SetFlags(0)
	log.SetOutput(std.Writer())
	return l
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 {
		switch a.Key {
		case slog.LevelKey:
			if lv, ok := a.Value.Any().(slog.Level); ok {
				a.Value = slog.StringValue(strings.ToLower(lv.String()))
			}
			return a
		case slog.TimeKey:
			return a
		case slog.MessageKey:
			a.Value = slog.StringValue(logsafe.RedactString(a.Value.String()))
			return a
		}
	}
	if sensitiveKey.MatchString(a.Key) {
		return slog.String(a.Key, redacted)
	}
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(logsafe.RedactString(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(logsafe.RedactString(err.Error()))
		}
	}
	return a
}

// Component returns a child of the default logger tagged with component.
func Component(name string) *slog.Logger {
	return slog.Default().With("component", name)
}

// WithCall returns a logger carrying call_uuid (empty uuid returns l).
func WithCall(l *slog.Logger, uuid string) *slog.Logger {
	if uuid == "" {
		return l
	}
	return l.With(CallUUID(uuid))
}

// CallUUID is the attribute used to correlate records of one call.
func CallUUID(uuid string) slog.Attr {
	return slog.String("call_uuid", uuid)
}

// Safe logs any value (struct, map, string) with credential fields masked
// by logsafe.Redact.
func Safe(key string, v interface{}) slog.Attr {
	return slog.String(key, logsafe.Redact(v))
}

// Fatal logs at error level and exits with status 1 (replacement for log.Fatal).
func Fatal(l *slog.Logger, msg string, args ...any) {
	l.Log(context.Background(), slog.LevelError, msg, args...)
	os.Exit(1)
}

// Panic logs at error level then panics with msg (replacement for log.Panic).
func Panic(l *slog.Logger, msg string, args ...any) {
	l.Log(context.Background(), slog.LevelError, msg, args...)
	panic(msg)
}
