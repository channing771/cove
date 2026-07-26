package harness

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/boxify/api-go/internal/core/llm"
)

// ErrCircuitOpen 表示熔断器打开，调用被快速失败。
var ErrCircuitOpen = errors.New("harness: circuit breaker open")

// BreakerConfig 配置熔断器。FailureThreshold<=0 时为恒等。
type BreakerConfig struct {
	FailureThreshold int
	OpenDuration     time.Duration
}

// withBreaker 返回按连续失败次数熔断非流式生成路径的中间件。
func withBreaker(cfg BreakerConfig) clientMiddleware {
	if cfg.FailureThreshold <= 0 {
		return func(c llm.Client) llm.Client { return c }
	}
	b := &breaker{cfg: cfg}
	return func(c llm.Client) llm.Client { return &breakerClient{Client: c, b: b} }
}

type breaker struct {
	cfg      BreakerConfig
	mu       sync.Mutex
	failures int
	openedAt time.Time
}

// allow 判断是否放行；打开态经 OpenDuration 后放行一次半开探测。
func (b *breaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures < b.cfg.FailureThreshold {
		return true
	}
	return now.Sub(b.openedAt) >= b.cfg.OpenDuration
}

func (b *breaker) record(now time.Time, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil {
		b.failures = 0
		return
	}
	b.failures++
	if b.failures >= b.cfg.FailureThreshold {
		b.openedAt = now
	}
}

type breakerClient struct {
	llm.Client
	b *breaker
}

func (c *breakerClient) Invoke(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (string, error) {
	if !c.b.allow(time.Now()) {
		return "", ErrCircuitOpen
	}
	s, err := c.Client.Invoke(ctx, m, o...)
	c.b.record(time.Now(), err)
	return s, err
}

func (c *breakerClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	if !c.b.allow(time.Now()) {
		return nil, ErrCircuitOpen
	}
	res, err := c.Client.InvokeResult(ctx, m, o...)
	c.b.record(time.Now(), err)
	return res, err
}
