package harness

import (
	"context"

	"github.com/boxify/api-go/internal/core/llm"
)

// scriptedClient 是实现 llm.Client 的测试桩，各方法行为可注入。
type scriptedClient struct {
	invoke       func(ctx context.Context) (string, error)
	invokeResult func(ctx context.Context) (*llm.LLMResult, error)
}

func (c *scriptedClient) Invoke(ctx context.Context, _ []*llm.Message, _ ...llm.ModelCallOption) (string, error) {
	if c.invoke != nil {
		return c.invoke(ctx)
	}
	return "ok", nil
}

func (c *scriptedClient) InvokeResult(ctx context.Context, _ []*llm.Message, _ ...llm.ModelCallOption) (*llm.LLMResult, error) {
	if c.invokeResult != nil {
		return c.invokeResult(ctx)
	}
	return &llm.LLMResult{Text: "ok"}, nil
}

func (c *scriptedClient) Stream(ctx context.Context, _ []*llm.Message, _ ...llm.ModelCallOption) (<-chan string, error) {
	ch := make(chan string)
	close(ch)
	return ch, nil
}

func (c *scriptedClient) Embed(ctx context.Context, _ []string, _ int, _ ...llm.EmbeddingOption) ([][]float64, error) {
	return nil, nil
}

func (c *scriptedClient) EmbedOne(ctx context.Context, _ string, _ int) ([]float64, error) {
	return nil, nil
}

// toolCallingClient 在 scriptedClient 基础上补齐 llm.ToolCallingClient。
type toolCallingClient struct {
	*scriptedClient
	invokeWithTools func(ctx context.Context) (*llm.LLMResult, error)
}

func (c toolCallingClient) InvokeWithTools(ctx context.Context, _ []*llm.Message, _ ...llm.ModelCallOption) (*llm.LLMResult, error) {
	if c.invokeWithTools != nil {
		return c.invokeWithTools(ctx)
	}
	return &llm.LLMResult{Text: "tc"}, nil
}
