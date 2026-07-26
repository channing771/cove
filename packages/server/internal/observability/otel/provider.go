package otel

import (
	"context"
	"strings"

	"github.com/boxify/api-go/internal/config"
	"github.com/boxify/api-go/internal/core/agent/harness"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const instrumentationName = "github.com/boxify/api-go/internal/core/agent/harness"

// Providers 持有装配好的 harness 适配器与关闭函数。
type Providers struct {
	Tracer   harness.Tracer
	Metrics  harness.Metrics
	shutdown func(context.Context) error
}

// Shutdown 优雅 flush 并关闭底层 provider。
func (p *Providers) Shutdown(ctx context.Context) error {
	if p == nil || p.shutdown == nil {
		return nil
	}
	return p.shutdown(ctx)
}

// Setup 依据配置装配 OTel 导出并返回 harness 适配器。
//
// cfg.Enabled 为 false 时返回 noop 适配器与空 Shutdown。未配置对应 endpoint 的信号
// （trace/metric）退化为 noop。exporter 采用惰性连接，坏 endpoint 不会导致 Setup 失败，
// 从而不阻断服务启动。
func Setup(ctx context.Context, cfg config.OTelConfig) (*Providers, error) {
	noop := &Providers{
		Tracer:   harness.NoopTracer{},
		Metrics:  harness.NoopMetrics{},
		shutdown: func(context.Context) error { return nil },
	}
	if !cfg.Enabled {
		return noop, nil
	}

	res := resource.NewSchemaless(attribute.String("service.name", serviceName(cfg)))
	var shutdowns []func(context.Context) error
	providers := &Providers{Tracer: harness.NoopTracer{}, Metrics: harness.NoopMetrics{}}

	if cfg.TracesEndpoint != "" {
		exp, err := otlptracehttp.New(ctx, traceOptions(cfg)...)
		if err != nil {
			return nil, err
		}
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
		)
		otel.SetTracerProvider(tp)
		providers.Tracer = NewTracer(tp.Tracer(instrumentationName))
		shutdowns = append(shutdowns, tp.Shutdown)
	}

	if cfg.MetricsEndpoint != "" {
		exp, err := otlpmetrichttp.New(ctx, metricOptions(cfg)...)
		if err != nil {
			return nil, err
		}
		mp := sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)),
			sdkmetric.WithResource(res),
		)
		otel.SetMeterProvider(mp)
		providers.Metrics = NewMetrics(mp.Meter(instrumentationName))
		shutdowns = append(shutdowns, mp.Shutdown)
	}

	providers.shutdown = chainShutdown(shutdowns)
	return providers, nil
}

func serviceName(cfg config.OTelConfig) string {
	if cfg.ServiceName != "" {
		return cfg.ServiceName
	}
	return "cove-api"
}

func traceOptions(cfg config.OTelConfig) []otlptracehttp.Option {
	opts := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(cfg.TracesEndpoint)}
	if headers := parseHeaders(cfg.TracesHeaders); len(headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(headers))
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	return opts
}

func metricOptions(cfg config.OTelConfig) []otlpmetrichttp.Option {
	opts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpointURL(cfg.MetricsEndpoint)}
	if headers := parseHeaders(cfg.MetricsHeaders); len(headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(headers))
	}
	if cfg.Insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	return opts
}

// parseHeaders 解析逗号分隔的 "Key=Value" 头串。
func parseHeaders(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func chainShutdown(shutdowns []func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		var firstErr error
		for _, fn := range shutdowns {
			if err := fn(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
}
