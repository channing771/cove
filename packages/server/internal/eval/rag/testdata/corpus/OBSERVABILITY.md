# LLM 监测（OpenTelemetry + Langfuse）

Cove 服务端通过 OpenTelemetry 对 Agent 运行埋点，并以 OTLP/HTTP 导出。**厂商中立**：改 OTLP endpoint
即可指向 Langfuse / Arize Phoenix / OpenObserve / SigNoz / Grafana Tempo 等任意后端，无需改代码。

## 埋点覆盖

Agent Harness 的 `observabilityHooks` 对每次运行产出：

- **Span**：`gen_ai.agent.run`（父）→ `gen_ai.chat`（模型调用）/ `gen_ai.execute_tool`（工具执行）子 span，
  带 `gen_ai.agent.name`、`gen_ai.tool.name`、`gen_ai.response.stop_reason`、`gen_ai.agent.iterations` 属性，
  错误自动置 span Error status。
- **Metrics**：`agent_runs_total{stopped_by}`、`agent_model_calls_total{status}`、
  `agent_tool_calls_total{tool,status}`、`agent_iterations_total`、`agent_errors_total`，
  以及 `agent_run_duration_seconds` / `agent_model_latency_seconds` / `agent_tool_latency_seconds` 直方图。

> 注：生产聊天走流式路径，token/成本用量当前不进 span（`gen_ai.usage.*` 待后续在非流式或
> stream-usage 增强补齐）。工具调用数、墙钟、时延、停止原因等护栏与信号均已覆盖。

## 快速开始（本地 Langfuse，开箱即用）

compose 已做 **headless 初始化**（自动建 org/project/账号 + 固定 demo 密钥）与 **MinIO bucket 自动创建**，无需进 UI 手动配置。

```bash
# 1. 起 Langfuse 栈（首次会拉镜像 + 迁移 ClickHouse，约 1-3 分钟）
docker compose -f deployments/docker-compose.observability.yml up -d
# 等 http://localhost:3000/api/public/health 返回 200

# 2. 用内置 demo 密钥计算 Basic auth（UI 登录：dev@cove.local / covedevpassword）
AUTH=$(printf 'pk-lf-cove-local-0000000000000000:sk-lf-cove-local-0000000000000000' | base64)
export OTEL_ENABLED=true
export OTEL_TRACES_ENDPOINT=http://localhost:3000/api/public/otel/v1/traces
export OTEL_TRACES_HEADERS="Authorization=Basic ${AUTH}"
export OTEL_INSECURE=true

# 3a. 冒烟验证（不启整套 API）：直接导一次真实 gen_ai span 到 Langfuse
go test ./internal/observability/otel/ -tags manual -run TestExportToLangfuse -v
#     然后 GET /api/public/traces 应看到一条 gen_ai.agent.run trace（含 gen_ai.chat 子 span）

# 3b. 或启动完整 API 并发起一次 chat，在 Langfuse 的 Tracing 看 run → model/tool trace
make api
```

> 已验证：clean `up -d` → bucket 自动创建 → web 健康 → OTLP 导出 → Langfuse 出现
> `gen_ai.agent.run`（含 `gen_ai.chat`）trace，属性 `gen_ai.agent.name=cove`、
> `gen_ai.response.stop_reason`、`gen_ai.agent.iterations`，全程零手动步骤。

## 配置项

`config.Observability.OTel`（YAML）/ 环境变量：

| YAML | Env | 说明 |
|---|---|---|
| `enabled` | `OTEL_ENABLED` | 总开关，默认 false |
| `service_name` | `OTEL_SERVICE_NAME` | resource `service.name`，默认 `cove-api` |
| `traces_endpoint` | `OTEL_TRACES_ENDPOINT` | 完整 OTLP/HTTP traces URL |
| `traces_headers` | `OTEL_TRACES_HEADERS` | 逗号分隔 `Key=Value` 头（Langfuse 用 Basic auth） |
| `metrics_endpoint` | `OTEL_METRICS_ENDPOINT` | 可选；留空则指标 noop（Langfuse 不摄取指标） |
| `metrics_headers` | `OTEL_METRICS_HEADERS` | 可选指标头 |
| `sample_ratio` | `OTEL_SAMPLE_RATIO` | 采样率 0..1，默认 1 |
| `insecure` | `OTEL_INSECURE` | 明文 HTTP/跳过 TLS |

**失败不阻断**：`OTEL_ENABLED=true` 但 endpoint 不可达时，导出器惰性连接、后台重试，不影响 API 启动与服务。

## 指向其他后端

- **Arize Phoenix**：`OTEL_TRACES_ENDPOINT=http://phoenix:6006/v1/traces`
- **OpenObserve / SigNoz / Grafana**：指向其 OTLP/HTTP traces（`.../v1/traces`）与（如需）metrics（`.../v1/metrics`）端点，按需在 headers 里带鉴权。
