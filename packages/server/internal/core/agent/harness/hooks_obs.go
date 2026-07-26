package harness

import (
	"context"
	"log/slog"
	"sync"
	"time"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/llm"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

// newObservabilityHooks 构造可观测 hooks，发射 metrics/trace 并打结构化日志。
//
// 每次 Run 应新建一份实例：内部用起始时间戳 map 计算时延，跨 Run 复用会串扰。
func newObservabilityHooks(m Metrics, tr Tracer) corereact.Hooks {
	if m == nil {
		m = NoopMetrics{}
	}
	if tr == nil {
		tr = NoopTracer{}
	}
	return &observabilityHooks{metrics: m, tracer: tr, starts: map[string]time.Time{}}
}

type observabilityHooks struct {
	corereact.NoopHooks
	metrics Metrics
	tracer  Tracer
	mu      sync.Mutex
	starts  map[string]time.Time
}

func (h *observabilityHooks) mark(key string) {
	h.mu.Lock()
	h.starts[key] = time.Now()
	h.mu.Unlock()
}

func (h *observabilityHooks) since(key string) float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.starts[key]
	if !ok {
		return 0
	}
	return time.Since(t).Seconds()
}

func (h *observabilityHooks) BeforeRun(_ context.Context, _ corereact.State) error {
	h.mark("run")
	return nil
}

func (h *observabilityHooks) AfterRun(ctx context.Context, result corereact.Result, runErr error) error {
	h.metrics.IncrCounter("agent_runs_total", map[string]string{"stopped_by": string(result.StoppedBy)})
	h.metrics.ObserveHistogram("agent_run_duration_seconds", h.since("run"), nil)
	slog.InfoContext(ctx, "agent run finished",
		"stopped_by", string(result.StoppedBy),
		"iterations", result.Iterations,
		"error", runErr,
	)
	return nil
}

func (h *observabilityHooks) BeforeModel(_ context.Context, _ corereact.State, _ []*llm.Message) error {
	h.mark("model")
	return nil
}

func (h *observabilityHooks) AfterModel(_ context.Context, _ corereact.State, _ string, modelErr error) error {
	status := "ok"
	if modelErr != nil {
		status = "error"
	}
	h.metrics.IncrCounter("agent_model_calls_total", map[string]string{"status": status})
	h.metrics.ObserveHistogram("agent_model_latency_seconds", h.since("model"), nil)
	return nil
}

func (h *observabilityHooks) BeforeTool(_ context.Context, _ corereact.State, call corereact.ToolCall) error {
	h.mark("tool:" + call.Name)
	return nil
}

func (h *observabilityHooks) AfterTool(_ context.Context, _ corereact.State, call corereact.ToolCall, _ coretool.Output, toolErr error) error {
	status := "ok"
	if toolErr != nil {
		status = "error"
	}
	h.metrics.IncrCounter("agent_tool_calls_total", map[string]string{"tool": call.Name, "status": status})
	h.metrics.ObserveHistogram("agent_tool_latency_seconds", h.since("tool:"+call.Name), map[string]string{"tool": call.Name})
	return nil
}

func (h *observabilityHooks) OnStep(_ context.Context, _ corereact.State, _ corereact.Step) error {
	h.metrics.IncrCounter("agent_iterations_total", nil)
	return nil
}

func (h *observabilityHooks) OnError(ctx context.Context, _ corereact.State, err error) error {
	h.metrics.IncrCounter("agent_errors_total", nil)
	slog.ErrorContext(ctx, "agent run error", "error", err)
	return nil
}
