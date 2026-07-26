package harness

// Metrics 是可观测计数与直方图的最小抽象，便于对接 Prometheus/OTel。
//
// labels 为 nil 时表示无标签。实现应对并发调用安全。
type Metrics interface {
	IncrCounter(name string, labels map[string]string)
	ObserveHistogram(name string, seconds float64, labels map[string]string)
}

// NoopMetrics 不记录任何指标。
type NoopMetrics struct{}

// IncrCounter 是空实现。
func (NoopMetrics) IncrCounter(string, map[string]string) {}

// ObserveHistogram 是空实现。
func (NoopMetrics) ObserveHistogram(string, float64, map[string]string) {}
