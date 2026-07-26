package otel

import (
	"context"
	"sync"

	"github.com/boxify/api-go/internal/core/agent/harness"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// NewMetrics 把 OpenTelemetry Meter 适配为 harness.Metrics。
//
// instrument 按名称惰性创建并缓存；创建失败的 instrument 记录为 nil 并静默跳过，
// 保证指标发射永不影响主流程。
func NewMetrics(meter metric.Meter) harness.Metrics {
	return &metricsAdapter{
		meter:      meter,
		counters:   map[string]metric.Int64Counter{},
		histograms: map[string]metric.Float64Histogram{},
	}
}

type metricsAdapter struct {
	meter      metric.Meter
	mu         sync.Mutex
	counters   map[string]metric.Int64Counter
	histograms map[string]metric.Float64Histogram
}

func (m *metricsAdapter) IncrCounter(name string, labels map[string]string) {
	c := m.counter(name)
	if c == nil {
		return
	}
	c.Add(context.Background(), 1, metric.WithAttributes(toAttributes(labels)...))
}

func (m *metricsAdapter) ObserveHistogram(name string, seconds float64, labels map[string]string) {
	h := m.histogram(name)
	if h == nil {
		return
	}
	h.Record(context.Background(), seconds, metric.WithAttributes(toAttributes(labels)...))
}

func (m *metricsAdapter) counter(name string) metric.Int64Counter {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.counters[name]; ok {
		return c
	}
	c, err := m.meter.Int64Counter(name)
	if err != nil {
		m.counters[name] = nil
		return nil
	}
	m.counters[name] = c
	return c
}

func (m *metricsAdapter) histogram(name string) metric.Float64Histogram {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.histograms[name]; ok {
		return h
	}
	h, err := m.meter.Float64Histogram(name)
	if err != nil {
		m.histograms[name] = nil
		return nil
	}
	m.histograms[name] = h
	return h
}

func toAttributes(labels map[string]string) []attribute.KeyValue {
	if len(labels) == 0 {
		return nil
	}
	attrs := make([]attribute.KeyValue, 0, len(labels))
	for k, v := range labels {
		attrs = append(attrs, attribute.String(k, v))
	}
	return attrs
}
