package otel

import (
	"context"
	"fmt"

	"github.com/boxify/api-go/internal/core/agent/harness"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// NewTracer 把 OpenTelemetry Tracer 适配为 harness.Tracer。
func NewTracer(t trace.Tracer) harness.Tracer {
	return &tracerAdapter{tracer: t}
}

type tracerAdapter struct {
	tracer trace.Tracer
}

func (a *tracerAdapter) StartSpan(ctx context.Context, name string) (context.Context, harness.Span) {
	ctx, span := a.tracer.Start(ctx, name)
	return ctx, &spanAdapter{span: span}
}

type spanAdapter struct {
	span trace.Span
}

// End 结束 span；err 非 nil 时记录错误并置 Error status。
func (s *spanAdapter) End(err error) {
	if err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(codes.Error, err.Error())
	}
	s.span.End()
}

// SetAttr 按值类型设置 span 属性。
func (s *spanAdapter) SetAttr(key string, value any) {
	s.span.SetAttributes(toKeyValue(key, value))
}

func toKeyValue(key string, value any) attribute.KeyValue {
	switch v := value.(type) {
	case string:
		return attribute.String(key, v)
	case bool:
		return attribute.Bool(key, v)
	case int:
		return attribute.Int(key, v)
	case int64:
		return attribute.Int64(key, v)
	case float64:
		return attribute.Float64(key, v)
	default:
		return attribute.String(key, fmt.Sprint(v))
	}
}
