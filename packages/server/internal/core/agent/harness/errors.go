package harness

import coreagent "github.com/boxify/api-go/internal/core/agent"

// stopError 把一个静态停止原因附加到 sentinel 错误上。
//
// 主循环收尾时通过 coreagent.StopReasonError 接口探测该错误，产出专属 StopReason。
type stopError struct {
	msg    string
	reason coreagent.StopReason
}

func (e *stopError) Error() string                         { return e.msg }
func (e *stopError) AgentStopReason() coreagent.StopReason { return e.reason }

var (
	// ErrBudgetExceeded 表示触达 token/成本/工具调用预算。
	ErrBudgetExceeded = &stopError{"harness: budget exceeded", coreagent.StopBudgetExceeded}
	// ErrDeadlineExceeded 表示触达墙钟时间上限。
	ErrDeadlineExceeded = &stopError{"harness: wall-clock deadline exceeded", coreagent.StopDeadlineExceeded}
	// ErrToolDenied 表示命中未授权工具且策略为硬停。
	ErrToolDenied = &stopError{"harness: tool denied by policy", coreagent.StopToolDenied}
)
