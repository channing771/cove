// Package otel 把 OpenTelemetry 接到 core/agent/harness 的 Metrics/Tracer 缝上。
//
// Setup 依据配置装配 TracerProvider/MeterProvider（OTLP over HTTP 导出），并返回实现
// harness.Metrics / harness.Tracer 的适配器；关闭时优雅 flush。导出走 HTTP 以兼容
// Langfuse（不支持 gRPC）。厂商中立：改 OTLP endpoint 即可指向 Langfuse/Phoenix/
// OpenObserve/SigNoz 等。
package otel
