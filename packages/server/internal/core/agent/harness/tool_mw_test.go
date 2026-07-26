package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	coretool "github.com/boxify/api-go/internal/core/tool"
)

func TestResilientTool_RecoversPanic(t *testing.T) {
	base := coretool.NewFuncTool(coretool.Descriptor{Name: "boom"}, func(context.Context, coretool.Input) (coretool.Output, error) {
		panic("kaboom")
	})
	wrapped := wrapTool(base, ToolResilienceConfig{})
	_, err := wrapped.Invoke(context.Background(), coretool.Input{})
	if err == nil {
		t.Fatal("panic 应转成 error 而非崩溃")
	}
}

func TestResilientTool_RetriesIdempotent(t *testing.T) {
	var calls int
	base := coretool.NewFuncTool(
		coretool.Descriptor{Name: "search", Annotations: map[string]any{"idempotent": true}},
		func(context.Context, coretool.Input) (coretool.Output, error) {
			calls++
			if calls < 2 {
				return coretool.Output{}, errors.New("flaky")
			}
			return coretool.Output{Text: "ok"}, nil
		})
	wrapped := wrapTool(base, ToolResilienceConfig{MaxAttempts: 3, BaseDelay: time.Millisecond})
	out, err := wrapped.Invoke(context.Background(), coretool.Input{})
	if err != nil || out.Text != "ok" || calls != 2 {
		t.Fatalf("calls=%d err=%v out=%v", calls, err, out)
	}
}

func TestResilientTool_NoRetryWhenNotIdempotent(t *testing.T) {
	var calls int
	base := coretool.NewFuncTool(coretool.Descriptor{Name: "write"}, func(context.Context, coretool.Input) (coretool.Output, error) {
		calls++
		return coretool.Output{}, errors.New("flaky")
	})
	wrapped := wrapTool(base, ToolResilienceConfig{MaxAttempts: 3, BaseDelay: time.Millisecond})
	_, _ = wrapped.Invoke(context.Background(), coretool.Input{})
	if calls != 1 {
		t.Fatalf("非幂等工具不应重试, calls=%d", calls)
	}
}

func TestResilientTool_Timeout(t *testing.T) {
	base := coretool.NewFuncTool(coretool.Descriptor{Name: "slow"}, func(ctx context.Context, _ coretool.Input) (coretool.Output, error) {
		select {
		case <-ctx.Done():
			return coretool.Output{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
			return coretool.Output{Text: "late"}, nil
		}
	})
	wrapped := wrapTool(base, ToolResilienceConfig{Timeout: 5 * time.Millisecond})
	_, err := wrapped.Invoke(context.Background(), coretool.Input{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}
