# LLM 监测接入设计（OpenTelemetry + Langfuse）

日期: 2026-07-26 · 分支: dev · 方案: A（OTel-native + OTLP 导出，厂商中立）

## 背景与问题

企业级 Agent Harness 预留了 `harness.Metrics` / `harness.Tracer` 接口 + noop 默认作为可观测适配缝，但：

- `observabilityHooks` 目前只用 metrics + slog，**Tracer 字段未真正起 span**（无 LLM 调用链路可视化）。
- 无 OTLP 导出，无法把 run/model/tool 数据送到任何 LLM 监测后端。

调研结论（2026-07）：**OpenTelemetry 是 LLM 可观测事实标准**（CNCF 已毕业，GenAI 语义约定 `gen_ai.*` 存在但仍 experimental）。主流自托管后端（Langfuse / Arize Phoenix / OpenObserve / SigNoz）几乎全部 OTLP-native。因此**选标准不选厂商**：用 OTel 埋点 + OTLP 导出，后端可无改代码切换。

## 决策（已确认）

1. 方案 **A**：OpenTelemetry Go SDK + OTLP 导出，实现 `harness.Metrics`/`harness.Tracer` 适配器，span 打 `gen_ai.*` 语义属性。
2. **包含 Langfuse 本地剖面**：`deployments/` 增可选 docker-compose profile 跑 Langfuse，OTLP 直连，开箱即见 trace/成本。
3. Langfuse 约束（调研确认）：**仅 OTLP over HTTP**（无 gRPC），端点 `…/api/public/otel/v1/traces`，Basic-auth 头；**只摄取 trace**（span→observation），不摄取 Prometheus 指标。故 trace exporter 指向 Langfuse；metric exporter 独立配置，未配置 metrics endpoint 时 harness 用 NoopMetrics。

## 目标架构

```
cmd/api/main:  config.Load → otel.Setup(cfg) → svc.New(…, providers) → defer Shutdown
                                   │
                    ┌──────────────┴───────────────┐
              TracerProvider(OTLP/HTTP)      MeterProvider(OTLP/HTTP, 可选)
                    │                              │
   harness.Tracer 适配器             harness.Metrics 适配器
                    │                              │
        observabilityHooks(增强：run/model/tool span + gen_ai 属性)
                    │
              OTLP/HTTP  ───────▶  Langfuse /api/public/otel/v1/traces (Basic auth)
                                   或 Phoenix / OpenObserve / SigNoz / Collector（改 endpoint）
```

## 新增包 `internal/observability/otel`

```go
// provider.go
type Providers struct {
    Tracer  harness.Tracer
    Metrics harness.Metrics
    shutdown func(context.Context) error
}
func Setup(ctx context.Context, cfg config.OTelConfig) (*Providers, error) // disabled→noop providers
func (p *Providers) Shutdown(ctx context.Context) error

// tracer_adapter.go —— 用 go.opentelemetry.io/otel/trace 实现 harness.Tracer
// metrics_adapter.go —— 用 go.opentelemetry.io/otel/metric 实现 harness.Metrics（按 name 缓存 instrument）
// attrs.go —— gen_ai.* 语义属性常量与构造辅助
```

- 依赖：`go.opentelemetry.io/otel/sdk`、`.../exporters/otlp/otlptrace/otlptracehttp`、`.../exporters/otlp/otlpmetric/otlpmetrichttp`（当前 otel API 已是 v1.29 间接依赖，补 SDK+exporter）。
- 导出器用 **HTTP**（`WithEndpointURL(fullURL)` + `WithHeaders`），兼容 Langfuse 限制。
- Resource 设 `service.name`、`service.version`。TracerProvider 用 batch span processor + `ParentBased(TraceIDRatio(sampleRatio))`。MeterProvider 用 periodic reader（仅当配置 metrics endpoint）。

## Harness 变更（`observabilityHooks` 增强，包内低风险）

`observabilityHooks` 增加 span 生命周期，父子经**存储的 run ctx** 手工串联（hooks 无法把派生 ctx 回注主循环，故不依赖 ctx 传播）：

- `BeforeRun`: `runCtx, runSpan = tracer.StartSpan(ctx, "gen_ai.agent.run")`，存 `runCtx`/`runSpan`；设 `gen_ai.agent.name`。
- `AfterRun`: 设 `gen_ai.response.stop_reason`、迭代数；`runSpan.End(runErr)`。
- `BeforeModel`/`AfterModel`: `_, mSpan = tracer.StartSpan(h.runCtx, "gen_ai.chat")`，`mSpan.End(modelErr)`。
- `BeforeTool`/`AfterTool`: `_, tSpan = tracer.StartSpan(h.runCtx, "gen_ai.execute_tool")`，设 `gen_ai.tool.name`，`tSpan.End(toolErr)`。
- 现有 metrics/slog 保留；新增 span 存储用 `sync.Mutex`，实例仍每次 Run 新建。

`harness.Span` 接口维持 `End(err)` / `SetAttr(k, v)` 不变；tracer 适配器在 `End(err)` 时按 err 设置 span status。

## 配置与装配

```go
// config.ObservabilityConfig（挂到 Config 顶层）
type OTelConfig struct {
    Enabled         bool
    ServiceName     string
    TracesEndpoint  string  // http://langfuse:3000/api/public/otel/v1/traces
    TracesHeaders   string  // "Authorization=Basic <base64(pk:sk)>"；亦可用 OTEL_EXPORTER_OTLP_HEADERS
    MetricsEndpoint string  // 可选；留空则 NoopMetrics
    SampleRatio     float64 // 0..1，默认 1
    Insecure        bool
}
```

- 默认 `Enabled=false`（不改现有部署行为）；env 覆盖：`OTEL_ENABLED`、`OTEL_TRACES_ENDPOINT`、`OTEL_TRACES_HEADERS`、`OTEL_SERVICE_NAME`、`OTEL_METRICS_ENDPOINT`、`OTEL_SAMPLE_RATIO`。
- `cmd/api/main`：`config.Load` 后 `providers, _ := otel.Setup(ctx, cfg.Observability.OTel)`，`defer providers.Shutdown(ctx)`；把 `providers.Tracer/Metrics` 传入 `svc.New`。
- `svc.ServiceContext` 持有 `HarnessTracer harness.Tracer` / `HarnessMetrics harness.Metrics`；chat `harnessOptions` 追加 `harness.WithMetrics/ WithTracer`。
- Setup 失败（如 endpoint 不可达）**不阻断启动**：记录 warn，退化为 noop，保证 API 可用。

## 本地 Langfuse 剖面

`deployments/docker-compose.observability.yml`（独立 profile `observability`）：Langfuse web + postgres +（clickhouse/redis/minio 按 Langfuse v3 最小集）。附 `.env.observability.example` 与 README 段落：生成 pk/sk → 计算 Basic auth → 设 `OTEL_TRACES_ENDPOINT`/`OTEL_TRACES_HEADERS` → 启动 API → 发起一次 chat → 在 Langfuse 看 run/model/tool trace。

## 边界与非目标

- **不改主循环**；harness 仅增强 `observabilityHooks`。
- token/成本属性：受流式路径限制（`observabilityHooks` 当前拿不到 Usage），本次 span 不含 `gen_ai.usage.*`；后续可在非流式路径或 stream-usage 增强补齐。文档标注。
- 不引入 Prometheus 原生 registry（OTel metrics 可经 OTLP→collector→Prometheus，避免双栈）。
- Langfuse 指标：Langfuse 不摄取指标，指标需 MetricsEndpoint 指向 collector/OTLP 指标后端。

## 测试策略

- otel 适配器：`Metrics` 适配器按 name 缓存 instrument（用真实 MeterProvider + manual reader 断言记录）；`Tracer` 适配器用 SDK `tracetest.SpanRecorder` 断言 span 名/属性/status。
- provider：`Setup(disabled)` 返回 noop 且 `Shutdown` 幂等；`Setup(enabled, 坏 endpoint)` 不返回致命错误（懒连接）。
- harness `observabilityHooks`：spy tracer 断言 run/model/tool span 起止与 `gen_ai.*` 属性、err→status。
- 配置：env 覆盖与默认值单测。
- 端到端：harness Run + SDK `tracetest` 断言产生 run→model/tool 的 span 树。

## 实施分期

0. config `OTelConfig` + env 覆盖 → 1. otel 包（provider + tracer/metrics 适配器 + attrs）→ 2. harness `observabilityHooks` span 增强 → 3. svc + cmd/api 装配（config 门控、失败不阻断）→ 4. Langfuse compose 剖面 + 文档 → 5. 全量校验（build/test/make docs）。
