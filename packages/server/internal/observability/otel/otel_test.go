package otel

import (
	"context"
	"errors"
	"testing"

	"github.com/boxify/api-go/internal/config"
	"github.com/boxify/api-go/internal/core/agent/harness"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTracerAdapter_RecordsSpanWithAttrAndStatus(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tracer := NewTracer(tp.Tracer("test"))

	_, span := tracer.StartSpan(context.Background(), harness.SpanExecuteTool)
	span.SetAttr(harness.AttrToolName, "search")
	span.End(errors.New("boom"))

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("应记录 1 个 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name() != harness.SpanExecuteTool {
		t.Fatalf("span 名错误: %q", s.Name())
	}
	if s.Status().Code != codes.Error {
		t.Fatalf("err 应置 Error status, got %v", s.Status().Code)
	}
	var found bool
	for _, kv := range s.Attributes() {
		if string(kv.Key) == harness.AttrToolName && kv.Value.AsString() == "search" {
			found = true
		}
	}
	if !found {
		t.Fatal("应带 gen_ai.tool.name=search 属性")
	}
}

func TestMetricsAdapter_DoesNotPanic(t *testing.T) {
	// 无 MeterProvider 装配时也应安全（用全局 noop meter）。
	m := NewMetrics(otelapi.Meter("test"))
	m.IncrCounter("agent_runs_total", map[string]string{"stopped_by": "final_answer"})
	m.ObserveHistogram("agent_run_duration_seconds", 0.5, nil)
}

func TestSetup_DisabledReturnsNoop(t *testing.T) {
	p, err := Setup(context.Background(), config.OTelConfig{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	// noop 适配器不应 panic，Shutdown 幂等。
	p.Metrics.IncrCounter("x", nil)
	_, span := p.Tracer.StartSpan(context.Background(), "x")
	span.End(nil)
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("noop Shutdown 应无错误: %v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown 应幂等: %v", err)
	}
}

func TestParseHeaders(t *testing.T) {
	h := parseHeaders("Authorization=Basic abc123, X-Extra = v ")
	if h["Authorization"] != "Basic abc123" {
		t.Fatalf("Authorization 解析错误: %q", h["Authorization"])
	}
	if h["X-Extra"] != "v" {
		t.Fatalf("X-Extra 解析错误: %q", h["X-Extra"])
	}
	if parseHeaders("") != nil {
		t.Fatal("空串应返回 nil")
	}
}
