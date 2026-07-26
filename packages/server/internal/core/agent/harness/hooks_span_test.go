package harness

import (
	"context"
	"errors"
	"sync"
	"testing"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

type spanRecord struct {
	name  string
	attrs map[string]any
	err   error
	ended bool
}

type spyTracer struct {
	mu    sync.Mutex
	spans []*spanRecord
}

func (s *spyTracer) StartSpan(ctx context.Context, name string) (context.Context, Span) {
	rec := &spanRecord{name: name, attrs: map[string]any{}}
	s.mu.Lock()
	s.spans = append(s.spans, rec)
	s.mu.Unlock()
	return ctx, &spySpan{rec: rec}
}

func (s *spyTracer) find(name string) *spanRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.spans {
		if r.name == name {
			return r
		}
	}
	return nil
}

type spySpan struct{ rec *spanRecord }

func (s *spySpan) End(err error)           { s.rec.err = err; s.rec.ended = true }
func (s *spySpan) SetAttr(k string, v any) { s.rec.attrs[k] = v }

func TestObservabilityHooks_StartsRunModelToolSpans(t *testing.T) {
	tr := &spyTracer{}
	h := newObservabilityHooks(NoopMetrics{}, tr)

	_ = h.BeforeRun(context.Background(), corereact.State{})
	_ = h.BeforeModel(context.Background(), corereact.State{}, nil)
	_ = h.AfterModel(context.Background(), corereact.State{}, "", nil)
	call := corereact.ToolCall{Name: "search"}
	_ = h.BeforeTool(context.Background(), corereact.State{}, call)
	_ = h.AfterTool(context.Background(), corereact.State{}, call, coretool.Output{}, errors.New("tool boom"))
	_ = h.AfterRun(context.Background(), corereact.Result{StoppedBy: corereact.StopFinalAnswer, Iterations: 1}, nil)

	run := tr.find(SpanAgentRun)
	if run == nil || !run.ended {
		t.Fatal("应创建并结束 run span")
	}
	if run.attrs[AttrStopReason] != string(corereact.StopFinalAnswer) {
		t.Fatalf("run span 应带 stop_reason, got %v", run.attrs[AttrStopReason])
	}
	if run.attrs[AttrAgentName] != agentName {
		t.Fatal("run span 应带 agent.name")
	}
	model := tr.find(SpanChat)
	if model == nil || !model.ended {
		t.Fatal("应创建并结束 model span")
	}
	tool := tr.find(SpanExecuteTool)
	if tool == nil || !tool.ended {
		t.Fatal("应创建并结束 tool span")
	}
	if tool.attrs[AttrToolName] != "search" {
		t.Fatalf("tool span 应带 tool.name, got %v", tool.attrs[AttrToolName])
	}
	if tool.err == nil {
		t.Fatal("tool span 应携带工具错误")
	}
}
