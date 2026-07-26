package harness

import (
	"context"
	"time"

	"github.com/boxify/api-go/internal/core/llm"
)

// withTimeout 返回对非流式生成路径施加单调用超时的中间件。d<=0 时为恒等。
func withTimeout(d time.Duration) clientMiddleware {
	if d <= 0 {
		return func(c llm.Client) llm.Client { return c }
	}
	return func(c llm.Client) llm.Client { return &timeoutClient{Client: c, d: d} }
}

type timeoutClient struct {
	llm.Client
	d time.Duration
}

func (t *timeoutClient) Invoke(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, t.d)
	defer cancel()
	return t.Client.Invoke(ctx, m, o...)
}

func (t *timeoutClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	ctx, cancel := context.WithTimeout(ctx, t.d)
	defer cancel()
	return t.Client.InvokeResult(ctx, m, o...)
}
