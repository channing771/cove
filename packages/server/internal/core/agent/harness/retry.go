package harness

import (
	"context"
	"errors"
	"time"

	coreagent "github.com/boxify/api-go/internal/core/agent"
	"github.com/boxify/api-go/internal/core/llm"
)

// RetryConfig 配置模型调用重试。
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Retryable   func(error) bool
}

// withRetry 返回对非流式生成路径重试的中间件。MaxAttempts<=1 时为恒等。
func withRetry(cfg RetryConfig) clientMiddleware {
	if cfg.MaxAttempts <= 1 {
		return func(c llm.Client) llm.Client { return c }
	}
	if cfg.Retryable == nil {
		cfg.Retryable = DefaultRetryable
	}
	return func(c llm.Client) llm.Client { return &retryClient{Client: c, cfg: cfg} }
}

type retryClient struct {
	llm.Client
	cfg RetryConfig
}

func (r *retryClient) Invoke(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (string, error) {
	return retryCall(ctx, r.cfg, func(ctx context.Context) (string, error) {
		return r.Client.Invoke(ctx, m, o...)
	})
}

func (r *retryClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return retryCall(ctx, r.cfg, func(ctx context.Context) (*llm.LLMResult, error) {
		return r.Client.InvokeResult(ctx, m, o...)
	})
}

// retryCall 对 fn 施加指数退避重试；ctx 取消时立即返回。
func retryCall[T any](ctx context.Context, cfg RetryConfig, fn func(context.Context) (T, error)) (T, error) {
	var zero, out T
	var last error
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		out, last = fn(ctx)
		if last == nil || !cfg.Retryable(last) {
			return out, last
		}
		if attempt == cfg.MaxAttempts {
			break
		}
		if err := retryWait(ctx, cfg, attempt); err != nil {
			return zero, err
		}
	}
	return zero, last
}

func retryWait(ctx context.Context, cfg RetryConfig, attempt int) error {
	delay := cfg.BaseDelay << (attempt - 1)
	if cfg.MaxDelay > 0 && delay > cfg.MaxDelay {
		delay = cfg.MaxDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// DefaultRetryable 对瞬时错误返回 true；ctx 取消/超时与熔断打开视为不可重试。
func DefaultRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, ErrCircuitOpen) {
		return false
	}
	// 治理类错误（预算/墙钟/门禁）是终止条件，重试无意义。
	var sre coreagent.StopReasonError
	if errors.As(err, &sre) {
		return false
	}
	return true
}
