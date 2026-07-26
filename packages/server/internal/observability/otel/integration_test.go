package otel

import (
	"context"
	"testing"

	"github.com/boxify/api-go/internal/core/agent/harness"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/llm"
	coretool "github.com/boxify/api-go/internal/core/tool"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// finalAnswerClient 是最简 llm.Client，文本 ReAct 路径下直接给出最终答案。
type finalAnswerClient struct{}

func (finalAnswerClient) Invoke(context.Context, []*llm.Message, ...llm.ModelCallOption) (string, error) {
	return "Final Answer: done", nil
}
func (finalAnswerClient) InvokeResult(context.Context, []*llm.Message, ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return &llm.LLMResult{Text: "Final Answer: done"}, nil
}
func (finalAnswerClient) Stream(context.Context, []*llm.Message, ...llm.ModelCallOption) (<-chan string, error) {
	ch := make(chan string)
	close(ch)
	return ch, nil
}
func (finalAnswerClient) Embed(context.Context, []string, int, ...llm.EmbeddingOption) ([][]float64, error) {
	return nil, nil
}
func (finalAnswerClient) EmbedOne(context.Context, string, int) ([]float64, error) { return nil, nil }

// TestHarnessRun_ProducesOTelSpans 端到端验证：harness Run 经 otel Tracer 适配器产出 run/model span。
func TestHarnessRun_ProducesOTelSpans(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tracer := NewTracer(tp.Tracer("test"))

	h := harness.New(finalAnswerClient{}, coretool.NewRegistry(), harness.WithTracer(tracer))
	res, err := h.Run(context.Background(), corereact.Input{Query: "hi"})
	if err != nil {
		t.Fatalf("run err=%v", err)
	}
	if res.StoppedBy != corereact.StopFinalAnswer {
		t.Fatalf("应正常结束, got %v", res.StoppedBy)
	}

	var hasRun, hasModel bool
	for _, s := range rec.Ended() {
		switch s.Name() {
		case harness.SpanAgentRun:
			hasRun = true
		case harness.SpanChat:
			hasModel = true
		}
	}
	if !hasRun || !hasModel {
		t.Fatalf("应产出 run 与 model span, run=%v model=%v", hasRun, hasModel)
	}
}
