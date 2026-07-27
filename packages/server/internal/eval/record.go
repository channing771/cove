package eval

import (
	"context"
	"time"

	"github.com/boxify/api-go/internal/core/agent/harness"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/llm"
)

// Usage 表示一次运行累计的 token 与折算成本。
type Usage struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// RunRecord 表示 SUT 跑完一条用例的产物。所有访问器对 Result==nil 安全。
type RunRecord struct {
	Result  *corereact.Result
	Latency time.Duration
	Usage   Usage
	Err     error
}

// Answer 返回最终答案;Result 为 nil 时返回空串。
func (r RunRecord) Answer() string {
	if r.Result == nil {
		return ""
	}
	return r.Result.Answer
}

// Iterations 返回迭代次数;Result 为 nil 时返回 0。
func (r RunRecord) Iterations() int {
	if r.Result == nil {
		return 0
	}
	return r.Result.Iterations
}

// StopReason 返回停止原因字符串;Result 为 nil 时返回空串。
func (r RunRecord) StopReason() string {
	if r.Result == nil {
		return ""
	}
	return string(r.Result.StoppedBy)
}

// ToolTrajectory 返回按序调用的工具名(跳过无 Action 的终局步)。
func (r RunRecord) ToolTrajectory() []string {
	if r.Result == nil {
		return nil
	}
	var out []string
	for _, s := range r.Result.Steps {
		if s.Action != "" {
			out = append(out, s.Action)
		}
	}
	return out
}

// usageClient 装饰非流式结构化生成路径,累加 token 与成本。仅暴露 llm.Client 核心能力。
type usageClient struct {
	llm.Client
	usage *Usage
	cost  harness.CostModel
}

func (c *usageClient) add(res *llm.LLMResult) {
	if res == nil {
		return
	}
	u := res.Usage
	c.usage.InputTokens += u.InputTokens
	c.usage.OutputTokens += u.OutputTokens
	c.usage.TotalTokens += u.TotalTokens
	if c.cost != nil {
		c.usage.CostUSD += c.cost.Cost(res.Model, u)
	}
}

func (c *usageClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	res, err := c.Client.InvokeResult(ctx, m, o...)
	if err != nil {
		return res, err
	}
	c.add(res)
	return res, nil
}

// usageToolClient 额外装饰非流式原生工具调用路径。
type usageToolClient struct {
	*usageClient
	base llm.ToolCallingClient
}

func (c *usageToolClient) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	res, err := c.base.InvokeWithTools(ctx, m, o...)
	if err != nil {
		return res, err
	}
	c.add(res)
	return res, nil
}

// wrapUsage 返回仅暴露非流式能力的 usage 捕获装饰器与其累加指针。
//
// base 实现 llm.ToolCallingClient 时,返回值同样实现之(经 InvokeWithTools 捕获);
// 否则只暴露 llm.Client 核心方法。故意不暴露 Stream/Vision,使 eval 运行走非流式,
// 从而可捕获 token/成本(StreamEvent 不带 usage)。
func wrapUsage(base llm.Client, cost harness.CostModel) (llm.Client, *Usage) {
	u := &Usage{}
	uc := &usageClient{Client: base, usage: u, cost: cost}
	if tc, ok := base.(llm.ToolCallingClient); ok {
		return &usageToolClient{usageClient: uc, base: tc}, u
	}
	return uc, u
}
