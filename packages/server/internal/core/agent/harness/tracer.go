package harness

import "context"

// Tracer 起 span；返回派生 ctx 与 Span。
type Tracer interface {
	StartSpan(ctx context.Context, name string) (context.Context, Span)
}

// Span 表示一次追踪跨度。
type Span interface {
	End(err error)
	SetAttr(key string, value any)
}

// NoopTracer 不产生任何 span。
type NoopTracer struct{}

// StartSpan 返回原 ctx 与空 span。
func (NoopTracer) StartSpan(ctx context.Context, _ string) (context.Context, Span) {
	return ctx, NoopSpan{}
}

// NoopSpan 是空 span。
type NoopSpan struct{}

// End 是空实现。
func (NoopSpan) End(error) {}

// SetAttr 是空实现。
func (NoopSpan) SetAttr(string, any) {}
