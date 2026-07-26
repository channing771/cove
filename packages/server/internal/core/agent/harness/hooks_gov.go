package harness

import (
	"context"
	"time"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
)

// newGovernanceHooks 构造治理 hooks：工具调用计数 + 墙钟 deadline 检查。
//
// deadline 为零值时不做墙钟检查。工具/成本 token 预算分别由 Budget 与 budgetClient 承担；
// 本 hooks 只负责路径无关的工具调用计数与墙钟护栏（对流式路径同样生效）。
func newGovernanceHooks(b *Budget, deadline time.Time) corereact.Hooks {
	return &governanceHooks{budget: b, deadline: deadline}
}

type governanceHooks struct {
	corereact.NoopHooks
	budget   *Budget
	deadline time.Time
}

func (h *governanceHooks) BeforeTool(_ context.Context, _ corereact.State, _ corereact.ToolCall) error {
	if h.budget == nil {
		return nil
	}
	return h.budget.AddToolCall()
}

func (h *governanceHooks) BeforeTransition(_ context.Context, _ corereact.State, _ corereact.Transition) error {
	if !h.deadline.IsZero() && time.Now().After(h.deadline) {
		return ErrDeadlineExceeded
	}
	return nil
}
