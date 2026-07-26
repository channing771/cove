package harness

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

type recordHook struct {
	corereact.NoopHooks
	beforeRun *int
	failErr   error
}

func (h recordHook) BeforeRun(_ context.Context, _ corereact.State) error {
	if h.beforeRun != nil {
		*h.beforeRun++
	}
	return h.failErr
}

func TestMultiHooks_FanOut(t *testing.T) {
	var a, b int
	m := MultiHooks(recordHook{beforeRun: &a}, nil, recordHook{beforeRun: &b})
	if err := m.BeforeRun(context.Background(), corereact.State{}); err != nil {
		t.Fatal(err)
	}
	if a != 1 || b != 1 {
		t.Fatalf("a=%d b=%d", a, b)
	}
}

func TestMultiHooks_ShortCircuit(t *testing.T) {
	var b int
	sentinel := errors.New("stop")
	m := MultiHooks(recordHook{failErr: sentinel}, recordHook{beforeRun: &b})
	if err := m.BeforeRun(context.Background(), corereact.State{}); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if b != 0 {
		t.Fatal("首个 hook 出错后不应继续")
	}
}

func TestGovernanceHooks_ToolCallBudget(t *testing.T) {
	b := NewBudget(BudgetConfig{MaxToolCalls: 1})
	h := newGovernanceHooks(b, time.Time{})
	if err := h.BeforeTool(context.Background(), corereact.State{}, corereact.ToolCall{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := h.BeforeTool(context.Background(), corereact.State{}, corereact.ToolCall{Name: "x"}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("第2次工具调用应越界, got %v", err)
	}
}

func TestGovernanceHooks_Deadline(t *testing.T) {
	h := newGovernanceHooks(NewBudget(BudgetConfig{}), time.Now().Add(-time.Second))
	if err := h.BeforeTransition(context.Background(), corereact.State{}, corereact.Transition{}); !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("已过 deadline 应返回 ErrDeadlineExceeded, got %v", err)
	}
}

type spyMetrics struct {
	mu       sync.Mutex
	counters map[string]int
}

func (s *spyMetrics) IncrCounter(name string, _ map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.counters == nil {
		s.counters = map[string]int{}
	}
	s.counters[name]++
}

func (s *spyMetrics) ObserveHistogram(string, float64, map[string]string) {}

func (s *spyMetrics) count(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counters[name]
}

func TestObservabilityHooks_CountsToolCalls(t *testing.T) {
	m := &spyMetrics{}
	h := newObservabilityHooks(m, NoopTracer{})
	call := corereact.ToolCall{Name: "search"}
	_ = h.BeforeTool(context.Background(), corereact.State{}, call)
	_ = h.AfterTool(context.Background(), corereact.State{}, call, coretool.Output{}, nil)
	if m.count("agent_tool_calls_total") == 0 {
		t.Fatal("应发射 agent_tool_calls_total")
	}
}

func TestObservabilityHooks_CountsRunAndError(t *testing.T) {
	m := &spyMetrics{}
	h := newObservabilityHooks(m, NoopTracer{})
	_ = h.BeforeRun(context.Background(), corereact.State{})
	_ = h.OnError(context.Background(), corereact.State{}, errors.New("boom"))
	_ = h.AfterRun(context.Background(), corereact.Result{StoppedBy: corereact.StopError}, errors.New("boom"))
	if m.count("agent_runs_total") == 0 || m.count("agent_errors_total") == 0 {
		t.Fatalf("应发射 runs 与 errors 计数, counters=%v", m.counters)
	}
}
