package harness

import (
	"time"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/llm"
)

// Option 配置 Harness 的横切能力。
type Option func(*Harness)

// WithRetry 配置模型非流式路径重试。
func WithRetry(cfg RetryConfig) Option {
	return func(h *Harness) { h.retry = cfg }
}

// WithTimeout 配置模型非流式路径单调用超时。
func WithTimeout(d time.Duration) Option {
	return func(h *Harness) { h.timeout = d }
}

// WithBreaker 配置模型非流式路径熔断。
func WithBreaker(cfg BreakerConfig) Option {
	return func(h *Harness) { h.breaker = cfg }
}

// WithToolResilience 配置工具重试/超时/panic 恢复。
func WithToolResilience(cfg ToolResilienceConfig) Option {
	return func(h *Harness) { h.toolResilience = cfg }
}

// WithBudgetConfig 配置每次运行的资源预算。
func WithBudgetConfig(cfg BudgetConfig) Option {
	return func(h *Harness) { h.budget = cfg }
}

// WithCostModel 配置成本折算模型。
func WithCostModel(cost CostModel) Option {
	return func(h *Harness) { h.cost = cost }
}

// WithPolicy 配置工具治理策略（白名单 + 动态门禁）。
func WithPolicy(p Policy) Option {
	return func(h *Harness) { h.policy = p }
}

// WithWallClock 配置单次运行的墙钟上限；<=0 表示不限制。
func WithWallClock(d time.Duration) Option {
	return func(h *Harness) { h.wallClock = d }
}

// WithMetrics 配置可观测指标接收器。
func WithMetrics(m Metrics) Option {
	return func(h *Harness) {
		if m != nil {
			h.metrics = m
		}
	}
}

// WithTracer 配置追踪器。
func WithTracer(tr Tracer) Option {
	return func(h *Harness) {
		if tr != nil {
			h.tracer = tr
		}
	}
}

// WithDeterminism 配置确定性录制/回放模式与磁带文件路径。
func WithDeterminism(mode DeterminismMode, cassettePath string) Option {
	return func(h *Harness) {
		h.determinism = mode
		h.cassettePath = cassettePath
	}
}

// WithUserHooks 追加调用方自定义 hooks（与可观测/治理 hooks 组合）。
func WithUserHooks(hooks corereact.Hooks) Option {
	return func(h *Harness) {
		if hooks != nil {
			h.userHooks = hooks
		}
	}
}

// WithSystemPrompt 设置注入 react Agent 的系统提示词。
func WithSystemPrompt(prompt string) Option {
	return func(h *Harness) { h.systemPrompt = prompt }
}

// WithModelOptions 设置默认模型调用参数。
func WithModelOptions(opts ...llm.ModelCallOption) Option {
	return func(h *Harness) { h.modelOptions = append([]llm.ModelCallOption{}, opts...) }
}

// WithMaxIterations 设置默认最大 ReAct 迭代次数。
func WithMaxIterations(n int) Option {
	return func(h *Harness) { h.maxIterations = n }
}
