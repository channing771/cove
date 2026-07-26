package chat

import (
	"context"
	"strings"
	"time"

	"github.com/boxify/api-go/internal/config"
	"github.com/boxify/api-go/internal/core/agent/harness"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
	corecontext "github.com/boxify/api-go/internal/core/context"
	"github.com/boxify/api-go/internal/core/llm"
)

// chatEngine 抽象聊天运行引擎，裸 react.Agent 与企业级 Harness 均满足。
type chatEngine interface {
	Run(ctx context.Context, input corereact.Input, opts ...corereact.RunOption) (*corereact.Result, error)
}

// harnessOptions 把 HarnessConfig 与运行期参数映射为 harness 选项。
//
// contextManager 为 nil 时不注入 MessagePreparer（避免非空接口包裹空指针导致空调用）。
func harnessOptions(hc config.HarnessConfig, hooks corereact.Hooks, temperature float64, systemPrompt string, contextManager *corecontext.Manager, metrics harness.Metrics, tracer harness.Tracer) []harness.Option {
	opts := []harness.Option{
		harness.WithUserHooks(hooks),
		harness.WithSystemPrompt(strings.TrimSpace(systemPrompt)),
		harness.WithModelOptions(llm.WithTemperature(temperature)),
		harness.WithRetry(harness.RetryConfig{MaxAttempts: hc.RetryMaxAttempts, BaseDelay: msDuration(hc.RetryBaseMs)}),
		harness.WithTimeout(msDuration(hc.TimeoutMs)),
		harness.WithBreaker(harness.BreakerConfig{FailureThreshold: hc.BreakerFailures, OpenDuration: msDuration(hc.BreakerOpenMs)}),
		harness.WithBudgetConfig(harness.BudgetConfig{MaxTotalTokens: hc.MaxTotalTokens, MaxToolCalls: hc.MaxToolCalls}),
		harness.WithWallClock(msDuration(hc.WallClockMs)),
		harness.WithMetrics(metrics),
		harness.WithTracer(tracer),
	}
	if contextManager != nil {
		opts = append(opts, harness.WithMessagePreparer(contextManager))
	}
	return opts
}

// msDuration 把毫秒整数转为 time.Duration；<=0 返回 0（表示不限制/关闭）。
func msDuration(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
