package otel

// OpenTelemetry GenAI 语义约定属性键（截至 2026 仍 experimental，名称可能演进）。
const (
	AttrOperationName = "gen_ai.operation.name"
	AttrAgentName     = "gen_ai.agent.name"
	AttrToolName      = "gen_ai.tool.name"
	AttrStopReason    = "gen_ai.response.stop_reason"
	AttrIterations    = "gen_ai.agent.iterations"
)

// Span 名（对齐 GenAI 约定的 operation 命名）。
const (
	SpanAgentRun    = "gen_ai.agent.run"
	SpanChat        = "gen_ai.chat"
	SpanExecuteTool = "gen_ai.execute_tool"
)
