package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug, "DEBUG": slog.LevelDebug,
		"warn": slog.LevelWarn, "warning": slog.LevelWarn,
		"error": slog.LevelError, "info": slog.LevelInfo,
		"": slog.LevelInfo, "nonsense": slog.LevelInfo,
	}
	for name, want := range cases {
		if got := ParseLevel(name); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestJSONHandlerRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Level: "warn", JSON: true, Output: &buf})
	l.Info("suppressed")
	l.Warn("emitted", "payment_id", "pay_1")
	if strings.Contains(buf.String(), "suppressed") {
		t.Error("info record emitted at warn level")
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output is not JSON: %v (%s)", err, buf.String())
	}
	if rec["payment_id"] != "pay_1" {
		t.Errorf("attribute missing from record: %v", rec)
	}
}

func TestContextRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Level: "info", JSON: true, Output: &buf})
	ctx := With(WithLogger(context.Background(), l), "request_id", "req_1")
	FromContext(ctx).Info("hello")
	if !strings.Contains(buf.String(), `"request_id":"req_1"`) {
		t.Errorf("context attributes lost: %s", buf.String())
	}
}

func TestFromContextFallsBackToDefault(t *testing.T) {
	if FromContext(context.Background()) == nil {
		t.Fatal("FromContext returned nil for a bare context")
	}
}
