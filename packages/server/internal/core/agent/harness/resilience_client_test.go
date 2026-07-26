package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/boxify/api-go/internal/core/llm"
)

func TestRetryClient_RetriesTransientThenSucceeds(t *testing.T) {
	var calls int
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("temporary failure")
		}
		return &llm.LLMResult{Text: "ok"}, nil
	}}
	c := chainClient(base, withRetry(RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, Retryable: func(error) bool { return true }}))
	res, err := c.InvokeResult(context.Background(), nil)
	if err != nil || res.Text != "ok" || calls != 3 {
		t.Fatalf("calls=%d err=%v res=%v", calls, err, res)
	}
}

func TestRetryClient_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) { return nil, errors.New("x") }}
	c := chainClient(base, withRetry(RetryConfig{MaxAttempts: 5, BaseDelay: time.Second, Retryable: func(error) bool { return true }}))
	if _, err := c.InvokeResult(ctx, nil); err == nil {
		t.Fatal("已取消的 ctx 应尽快返回错误")
	}
}

func TestRetryClient_NonRetryableReturnsImmediately(t *testing.T) {
	var calls int
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) {
		calls++
		return nil, errors.New("permanent")
	}}
	c := chainClient(base, withRetry(RetryConfig{MaxAttempts: 5, BaseDelay: time.Millisecond, Retryable: func(error) bool { return false }}))
	_, _ = c.InvokeResult(context.Background(), nil)
	if calls != 1 {
		t.Fatalf("不可重试错误不应重试, calls=%d", calls)
	}
}

func TestTimeoutClient_CancelsSlowCall(t *testing.T) {
	base := &scriptedClient{invokeResult: func(ctx context.Context) (*llm.LLMResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
			return &llm.LLMResult{Text: "late"}, nil
		}
	}}
	c := chainClient(base, withTimeout(5*time.Millisecond))
	_, err := c.InvokeResult(context.Background(), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}

func TestBreaker_OpensAfterThreshold(t *testing.T) {
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) { return nil, errors.New("boom") }}
	c := chainClient(base, withBreaker(BreakerConfig{FailureThreshold: 2, OpenDuration: time.Minute}))
	_, _ = c.InvokeResult(context.Background(), nil)
	_, _ = c.InvokeResult(context.Background(), nil)
	_, err := c.InvokeResult(context.Background(), nil) // 第3次应快速失败
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("want ErrCircuitOpen, got %v", err)
	}
}

func TestBreaker_HalfOpenRecovers(t *testing.T) {
	fail := true
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) {
		if fail {
			return nil, errors.New("boom")
		}
		return &llm.LLMResult{Text: "ok"}, nil
	}}
	c := chainClient(base, withBreaker(BreakerConfig{FailureThreshold: 1, OpenDuration: time.Millisecond}))
	_, _ = c.InvokeResult(context.Background(), nil) // 打开
	time.Sleep(2 * time.Millisecond)
	fail = false
	res, err := c.InvokeResult(context.Background(), nil) // 半开探测成功
	if err != nil || res.Text != "ok" {
		t.Fatalf("半开应恢复, err=%v res=%v", err, res)
	}
}

func TestDefaultRetryable(t *testing.T) {
	if DefaultRetryable(nil) {
		t.Fatal("nil 不应重试")
	}
	if DefaultRetryable(context.Canceled) || DefaultRetryable(context.DeadlineExceeded) {
		t.Fatal("ctx 错误不应重试")
	}
	if DefaultRetryable(ErrCircuitOpen) {
		t.Fatal("熔断打开不应重试")
	}
	if !DefaultRetryable(errors.New("transient")) {
		t.Fatal("普通错误应重试")
	}
}
