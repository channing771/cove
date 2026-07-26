package harness

// OpenTelemetry GenAI 语义约定 span 名与属性键（截至 2026 仍 experimental，名称可能演进）。
// 定义在 harness 层，因 span 由 observabilityHooks 创建；具体导出后端（otel 包）复用这些常量。
const (
	// SpanAgentRun 表示一次完整 Agent 运行。
	SpanAgentRun = "gen_ai.agent.run"
	// SpanChat 表示一次模型调用。
	SpanChat = "gen_ai.chat"
	// SpanExecuteTool 表示一次工具执行。
	SpanExecuteTool = "gen_ai.execute_tool"

	// AttrAgentName 是 Agent 名。
	AttrAgentName = "gen_ai.agent.name"
	// AttrToolName 是被调用工具名。
	AttrToolName = "gen_ai.tool.name"
	// AttrStopReason 是运行停止原因。
	AttrStopReason = "gen_ai.response.stop_reason"
	// AttrIterations 是运行迭代数。
	AttrIterations = "gen_ai.agent.iterations"

	// agentName 是 span 上标注的固定 Agent 身份。
	agentName = "cove"
)
