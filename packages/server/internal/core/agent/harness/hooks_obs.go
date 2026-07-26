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

// newObservabilityHooks 构造可观测 hooks，发射 metrics、起 trace span 并打结构化日志。
//
// 每次 Run 应新建一份实例：内部用起始时间戳与活跃 span map 维护状态，跨 Run 复用会串扰。
// span 父子经存储的 runCtx 手工串联（hooks 无法把派生 ctx 回注主循环）。
func newObservabilityHooks(m Metrics, tr Tracer) corereact.Hooks {
	if m == nil {
		m = NoopMetrics{}
	}
	if tr == nil {
		tr = NoopTracer{}
	}
	return &observabilityHooks{
		metrics: m,
		tracer:  tr,
		starts:  map[string]time.Time{},
		spans:   map[string]Span{},
	}
}

type observabilityHooks struct {
	corereact.NoopHooks
	metrics Metrics
	tracer  Tracer
	mu      sync.Mutex
	starts  map[string]time.Time
	spans   map[string]Span
	runCtx  context.Context
	runSpan Span
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

// parentCtx 返回 run span 的 ctx 作为子 span 的父；无 run span 时回退传入 ctx。
func (h *observabilityHooks) parentCtx(ctx context.Context) context.Context {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runCtx != nil {
		return h.runCtx
	}
	return ctx
}

func (h *observabilityHooks) startSpan(key string, ctx context.Context, name string) Span {
	_, span := h.tracer.StartSpan(h.parentCtx(ctx), name)
	h.mu.Lock()
	h.spans[key] = span
	h.mu.Unlock()
	return span
}

func (h *observabilityHooks) endSpan(key string, err error) {
	h.mu.Lock()
	span := h.spans[key]
	delete(h.spans, key)
	h.mu.Unlock()
	if span != nil {
		span.End(err)
	}
}

func (h *observabilityHooks) BeforeRun(ctx context.Context, _ corereact.State) error {
	h.mark("run")
	runCtx, span := h.tracer.StartSpan(ctx, SpanAgentRun)
	span.SetAttr(AttrAgentName, agentName)
	h.mu.Lock()
	h.runCtx = runCtx
	h.runSpan = span
	h.mu.Unlock()
	return nil
}

func (h *observabilityHooks) AfterRun(ctx context.Context, result corereact.Result, runErr error) error {
	h.metrics.IncrCounter("agent_runs_total", map[string]string{"stopped_by": string(result.StoppedBy)})
	h.metrics.ObserveHistogram("agent_run_duration_seconds", h.since("run"), nil)
	h.mu.Lock()
	span := h.runSpan
	h.runSpan = nil
	h.mu.Unlock()
	if span != nil {
		span.SetAttr(AttrStopReason, string(result.StoppedBy))
		span.SetAttr(AttrIterations, result.Iterations)
		span.End(runErr)
	}
	slog.InfoContext(ctx, "agent run finished",
		"stopped_by", string(result.StoppedBy),
		"iterations", result.Iterations,
		"error", runErr,
	)
	return nil
}

func (h *observabilityHooks) BeforeModel(ctx context.Context, _ corereact.State, _ []*llm.Message) error {
	h.mark("model")
	h.startSpan("model", ctx, SpanChat)
	return nil
}

func (h *observabilityHooks) AfterModel(_ context.Context, _ corereact.State, _ string, modelErr error) error {
	status := "ok"
	if modelErr != nil {
		status = "error"
	}
	h.metrics.IncrCounter("agent_model_calls_total", map[string]string{"status": status})
	h.metrics.ObserveHistogram("agent_model_latency_seconds", h.since("model"), nil)
	h.endSpan("model", modelErr)
	return nil
}

func (h *observabilityHooks) BeforeTool(ctx context.Context, _ corereact.State, call corereact.ToolCall) error {
	h.mark("tool:" + call.Name)
	span := h.startSpan("tool:"+call.Name, ctx, SpanExecuteTool)
	span.SetAttr(AttrToolName, call.Name)
	return nil
}

func (h *observabilityHooks) AfterTool(_ context.Context, _ corereact.State, call corereact.ToolCall, _ coretool.Output, toolErr error) error {
	status := "ok"
	if toolErr != nil {
		status = "error"
	}
	h.metrics.IncrCounter("agent_tool_calls_total", map[string]string{"tool": call.Name, "status": status})
	h.metrics.ObserveHistogram("agent_tool_latency_seconds", h.since("tool:"+call.Name), map[string]string{"tool": call.Name})
	h.endSpan("tool:"+call.Name, toolErr)
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
