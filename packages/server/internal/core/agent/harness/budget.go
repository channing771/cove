package harness

import (
	"context"
	"sync"

	"github.com/boxify/api-go/internal/core/llm"
)

// BudgetConfig 定义单次运行的资源上限。零值字段表示不限制该维度。
type BudgetConfig struct {
	MaxInputTokens  int64
	MaxOutputTokens int64
	MaxTotalTokens  int64
	MaxCostUSD      float64
	MaxToolCalls    int
}

// Budget 线程安全地累计一次运行的 token、成本与工具调用消耗。
type Budget struct {
	cfg       BudgetConfig
	mu        sync.Mutex
	in        int64
	out       int64
	total     int64
	cost      float64
	toolCalls int
}

// NewBudget 创建预算追踪器。
func NewBudget(cfg BudgetConfig) *Budget { return &Budget{cfg: cfg} }

// AddUsage 累加一次模型用量与成本；任一维度越界返回 ErrBudgetExceeded。
func (b *Budget) AddUsage(u llm.TokenUsage, costUSD float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.in += u.InputTokens
	b.out += u.OutputTokens
	b.total += u.TotalTokens
	b.cost += costUSD
	c := b.cfg
	switch {
	case c.MaxInputTokens > 0 && b.in > c.MaxInputTokens,
		c.MaxOutputTokens > 0 && b.out > c.MaxOutputTokens,
		c.MaxTotalTokens > 0 && b.total > c.MaxTotalTokens,
		c.MaxCostUSD > 0 && b.cost > c.MaxCostUSD:
		return ErrBudgetExceeded
	}
	return nil
}

// AddToolCall 累加一次工具调用；越界返回 ErrBudgetExceeded。
func (b *Budget) AddToolCall() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.toolCalls++
	if b.cfg.MaxToolCalls > 0 && b.toolCalls > b.cfg.MaxToolCalls {
		return ErrBudgetExceeded
	}
	return nil
}

// BudgetSnapshot 是预算的只读快照。
type BudgetSnapshot struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostUSD      float64
	ToolCalls    int
}

// Snapshot 返回当前累计值快照。
func (b *Budget) Snapshot() BudgetSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BudgetSnapshot{b.in, b.out, b.total, b.cost, b.toolCalls}
}

// withBudget 返回在每次结构化生成后计费并检查预算的中间件。b 为 nil 时为恒等。
func withBudget(b *Budget, cost CostModel) clientMiddleware {
	if b == nil {
		return func(c llm.Client) llm.Client { return c }
	}
	if cost == nil {
		cost = CostTable{}
	}
	return func(c llm.Client) llm.Client { return &budgetClient{Client: c, b: b, cost: cost} }
}

type budgetClient struct {
	llm.Client
	b    *Budget
	cost CostModel
}

func (c *budgetClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	res, err := c.Client.InvokeResult(ctx, m, o...)
	if err != nil {
		return res, err
	}
	if berr := c.b.AddUsage(res.Usage, c.cost.Cost(res.Model, res.Usage)); berr != nil {
		return res, berr
	}
	return res, nil
}
