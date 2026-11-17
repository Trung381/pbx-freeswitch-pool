package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func decode(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid JSON %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug, "INFO": slog.LevelInfo, "warn": slog.LevelWarn,
		"warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q)=%v want %v", in, got, want)
		}
	}
}

func TestLevelFromEnv(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	if LevelFromEnv() != slog.LevelDebug {
		t.Fatal("expected debug")
	}
	t.Setenv("LOG_LEVEL", "")
	if LevelFromEnv() != slog.LevelInfo {
		t.Fatal("expected default info")
	}
}

func TestJSONOutputAndLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	l := Setup(&buf, slog.LevelInfo)
	c := WithCall(l.With("component", "fsesl"), "uuid-1")
	c.Debug("hidden")
	c.Info("hello", "n", 1)
	c.Error("bad", "err", errors.New("boom"))
	recs := decode(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d: %s", len(recs), buf.String())
	}
	r := recs[0]
	if r["level"] != "info" || r["msg"] != "hello" || r["service"] != "custompbx" ||
		r["component"] != "fsesl" || r["call_uuid"] != "uuid-1" || r["n"] != float64(1) {
		t.Fatalf("unexpected record: %v", r)
	}
	if recs[1]["level"] != "error" || recs[1]["err"] != "boom" {
		t.Fatalf("unexpected error record: %v", recs[1])
	}
}

func TestDebugLevelEnabled(t *testing.T) {
	var buf bytes.Buffer
	l := Setup(&buf, slog.LevelDebug)
	l.Debug("shown")
	if len(decode(t, &buf)) != 1 {
		t.Fatal("debug record missing")
	}
}

func TestRedaction(t *testing.T) {
	var buf bytes.Buffer
	l := Setup(&buf, slog.LevelInfo)
	l.Info("dsn postgres://u:hunter2@db/x", "password", "p1", "auth_token", "t1",
		"url", "http://a:s3cret@host/p", "err", errors.New("password=zzz failed"),
		Safe("payload", map[string]any{"token": "abc", "ok": "visible"}))
	out := buf.String()
	for _, s := range []string{"hunter2", "p1\"", "t1\"", "s3cret", "zzz", "abc"} {
		if strings.Contains(out, s) {
			t.Fatalf("leaked %q in %s", s, out)
		}
	}
	if !strings.Contains(out, "visible") || !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("unexpected output %s", out)
	}
}

func TestStdLogBridge(t *testing.T) {
	var buf bytes.Buffer
	Setup(&buf, slog.LevelInfo)
	defer Setup(&bytes.Buffer{}, slog.LevelInfo)
	log.Printf("third party says %s", "hi")
	recs := decode(t, &buf)
	if len(recs) != 1 || recs[0]["msg"] != "third party says hi" || recs[0]["component"] != "stdlog" || recs[0]["service"] != "custompbx" {
		t.Fatalf("unexpected bridge output: %s", buf.String())
	}
}

func TestHandlerInjectsTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewHandler(&buf, slog.LevelInfo))
	tid, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	sid, _ := trace.SpanIDFromHex("0123456789abcdef")
	ctx := trace.ContextWithSpanContext(context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
	l.InfoContext(ctx, "with span")
	l.Info("without span")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines=%d", len(lines))
	}
	if !strings.Contains(lines[0], `"trace_id":"0123456789abcdef0123456789abcdef"`) || !strings.Contains(lines[0], `"span_id":"0123456789abcdef"`) {
		t.Errorf("missing ids: %s", lines[0])
	}
	if strings.Contains(lines[1], "trace_id") {
		t.Errorf("unexpected ids: %s", lines[1])
	}
}
