package harness

import (
	"context"
	"time"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/llm"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

// Harness 用可靠性、治理、可观测、确定性能力装配一个即用的 react.Agent。
//
// 装配不修改 react 主循环：client 经装饰器链包裹，工具经门禁+可靠性装饰，
// 生命周期 hooks 经 MultiHooks 组合。Budget 与可观测计时是每次运行独立的。
type Harness struct {
	client   llm.Client
	registry *coretool.Registry

	retry          RetryConfig
	timeout        time.Duration
	breaker        BreakerConfig
	toolResilience ToolResilienceConfig
	budget         BudgetConfig
	cost           CostModel
	policy         Policy
	wallClock      time.Duration
	metrics        Metrics
	tracer         Tracer
	determinism    DeterminismMode
	cassettePath   string
	userHooks      corereact.Hooks
	systemPrompt   string
	modelOptions   []llm.ModelCallOption
	maxIterations  int
}

// New 创建 Harness。client 为底层模型客户端，registry 为业务工具注册表。
func New(client llm.Client, registry *coretool.Registry, opts ...Option) *Harness {
	h := &Harness{
		client:   client,
		registry: registry,
		metrics:  NoopMetrics{},
		tracer:   NoopTracer{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

// Run 执行一次带企业级护栏的 Agent 运行。
//
// 每次运行独立构造 Budget、可观测计时与（回放/录制）磁带；Record 模式在运行结束后落盘磁带。
func (h *Harness) Run(ctx context.Context, input corereact.Input, runOpts ...corereact.RunOption) (*corereact.Result, error) {
	budget := NewBudget(h.budget)
	client, saveCassette, err := h.buildClient(budget)
	if err != nil {
		return nil, err
	}
	registry, err := h.buildRegistry(ctx)
	if err != nil {
		return nil, err
	}
	hooks := h.buildHooks(budget)

	agent := corereact.New(client, registry, h.reactOptions(hooks)...)
	result, runErr := agent.Run(ctx, input, runOpts...)
	if saveCassette != nil {
		if serr := saveCassette(); serr != nil && runErr == nil {
			return result, serr
		}
	}
	return result, runErr
}

// buildClient 按 base→budget→timeout→retry→breaker→determinism 的顺序装饰客户端。
// saveCassette 非 nil 时（Record 模式）应在运行后调用以落盘磁带。
func (h *Harness) buildClient(budget *Budget) (llm.Client, func() error, error) {
	mws := []clientMiddleware{
		withBudget(budget, h.cost),
		withTimeout(h.timeout),
		withRetry(h.retry),
		withBreaker(h.breaker),
	}
	var saveCassette func() error
	switch h.determinism {
	case DeterminismRecord:
		cassette := &Cassette{}
		mws = append(mws, withRecord(cassette))
		if h.cassettePath != "" {
			saveCassette = func() error { return cassette.Save(h.cassettePath) }
		}
	case DeterminismReplay:
		cassette, err := LoadCassette(h.cassettePath)
		if err != nil {
			return nil, nil, err
		}
		mws = append(mws, withReplay(cassette, true))
	}
	return chainClient(h.client, mws...), saveCassette, nil
}

// buildRegistry 生成装饰后的工具注册表：白名单过滤 + 门禁 + 可靠性。
// 底层 registry 为 nil 时返回空注册表。
func (h *Harness) buildRegistry(ctx context.Context) (*coretool.Registry, error) {
	out := coretool.NewRegistry()
	if h.registry == nil {
		return out, nil
	}
	for _, tool := range h.registry.Tools(nil) {
		d, err := tool.Describe(ctx)
		if err != nil {
			return nil, err
		}
		if !h.policy.allowed(d.Name) {
			continue
		}
		decorated := wrapTool(guardTool(tool, h.policy), h.toolResilience)
		if err := out.Register(ctx, decorated); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// buildHooks 组合可观测、治理与用户 hooks。
func (h *Harness) buildHooks(budget *Budget) corereact.Hooks {
	var deadline time.Time
	if h.wallClock > 0 {
		deadline = time.Now().Add(h.wallClock)
	}
	hooks := []corereact.Hooks{
		newObservabilityHooks(h.metrics, h.tracer),
		newGovernanceHooks(budget, deadline),
	}
	if h.userHooks != nil {
		hooks = append(hooks, h.userHooks)
	}
	return MultiHooks(hooks...)
}

func (h *Harness) reactOptions(hooks corereact.Hooks) []corereact.Option {
	opts := []corereact.Option{corereact.WithHooks(hooks)}
	if h.systemPrompt != "" {
		opts = append(opts, corereact.WithSystemPrompt(h.systemPrompt))
	}
	if len(h.modelOptions) > 0 {
		opts = append(opts, corereact.WithModelOptions(h.modelOptions...))
	}
	if h.maxIterations > 0 {
		opts = append(opts, corereact.WithMaxIterations(h.maxIterations))
	}
	return opts
}
