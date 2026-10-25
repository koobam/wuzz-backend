package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestTraceHandlerStampsIDsOnlyWhenContextHasASpan(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(traceHandler{slog.NewJSONHandler(&buf, nil)})

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{2},
		TraceFlags: trace.FlagsSampled,
	})
	log.InfoContext(trace.ContextWithSpanContext(context.Background(), sc), "with span")
	log.InfoContext(context.Background(), "without span")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if !strings.Contains(lines[0], `"trace_id":"`+sc.TraceID().String()+`"`) || !strings.Contains(lines[0], `"span_id"`) {
		t.Errorf("record with a span has no ids: %s", lines[0])
	}
	if strings.Contains(lines[1], "trace_id") {
		t.Errorf("record without a span got ids: %s", lines[1])
	}
}
