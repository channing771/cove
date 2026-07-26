package harness

import "github.com/boxify/api-go/internal/core/llm"

// ModelRate 表示单个模型每 1K token 的输入/输出美元单价。
type ModelRate struct {
	InputPer1K  float64
	OutputPer1K float64
}

// CostModel 把一次用量折算为美元成本。
type CostModel interface {
	Cost(model string, u llm.TokenUsage) float64
}

// CostTable 按模型名查表折算成本；未知模型返回 0。
type CostTable map[string]ModelRate

// Cost 计算 input/output token 的成本之和。
func (t CostTable) Cost(model string, u llm.TokenUsage) float64 {
	rate, ok := t[model]
	if !ok {
		return 0
	}
	return float64(u.InputTokens)/1000*rate.InputPer1K + float64(u.OutputTokens)/1000*rate.OutputPer1K
}
