package harness

import (
	"context"
	"fmt"
	"time"

	coretool "github.com/boxify/api-go/internal/core/tool"
)

// ToolResilienceConfig 配置工具调用的重试与超时。
type ToolResilienceConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Timeout     time.Duration
}

// isIdempotent 依据 Descriptor.Annotations["idempotent"]==true 判定工具可安全重试。
func isIdempotent(d coretool.Descriptor) bool {
	v, _ := d.Annotations["idempotent"].(bool)
	return v
}

// wrapTool 为工具增加 panic 恢复、单调用超时，并对幂等工具重试。
func wrapTool(t coretool.Tool, cfg ToolResilienceConfig) coretool.Tool {
	return &resilientTool{inner: t, cfg: cfg}
}

type resilientTool struct {
	inner coretool.Tool
	cfg   ToolResilienceConfig
}

func (r *resilientTool) Describe(ctx context.Context) (coretool.Descriptor, error) {
	return r.inner.Describe(ctx)
}

func (r *resilientTool) Invoke(ctx context.Context, input coretool.Input) (coretool.Output, error) {
	d, _ := r.inner.Describe(ctx)
	attempts := 1
	if r.cfg.MaxAttempts > 1 && isIdempotent(d) {
		attempts = r.cfg.MaxAttempts
	}
	var out coretool.Output
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		out, err = r.invokeOnce(ctx, input)
		if err == nil {
			return out, nil
		}
		if attempt == attempts {
			break
		}
		delay := r.cfg.BaseDelay << (attempt - 1)
		if r.cfg.MaxDelay > 0 && delay > r.cfg.MaxDelay {
			delay = r.cfg.MaxDelay
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return coretool.Output{}, ctx.Err()
		case <-timer.C:
		}
	}
	return out, err
}

// invokeOnce 执行单次调用，捕获 panic 并施加可选超时。
func (r *resilientTool) invokeOnce(ctx context.Context, input coretool.Input) (out coretool.Output, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("tool panic: %v", p)
			out = coretool.Output{}
		}
	}()
	if r.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
	}
	return r.inner.Invoke(ctx, input)
}
