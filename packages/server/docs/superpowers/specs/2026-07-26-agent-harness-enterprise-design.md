# 企业级 Agent Harness 设计

日期: 2026-07-26 · 分支: dev · 方案: A（装饰器 + 组合 Hooks 的独立 harness 层，核心循环极小改动）

## 背景与问题

`internal/core/agent` + `internal/core/agent/react` 已提供成熟的业务无关 ReAct 引擎（双路径决策、状态机、生命周期 Hooks）。但从其 README 的 "Failure Behavior" 可见明确的企业级缺口：

- **可靠性**：模型/工具调用失败即终止，不重试、无退避、无超时、无熔断、无 panic 恢复。
- **治理**：无 token/成本预算、无墙钟上限、无最大工具调用数、无工具白名单/授权门禁。
- **可观测**：有 Hooks 扩展点，但无 metrics/trace/结构化日志适配器。
- **确定性**：无录制-回放，回归与可复现 trace 依赖真实 provider。

目标：在**不破坏 "core 业务无关" 规则、不改写成熟主循环**的前提下，为当前服务端补齐上述四支柱，并接入 `domain/flow/chat` 证明端到端可用。

## 决策（已确认）

1. 架构：**方案 A** —— 新增业务无关包 `internal/core/agent/harness`，用三种横切机制包裹 `react.Agent`：client 装饰器链、tool 装饰器、组合 Hooks。
2. 支柱：**可靠性 + 治理 + 可观测 + 确定性** 四者全纳入。
3. 工具门禁默认：**拒绝观察结果**（未授权工具返回一条 "不允许调用" 观察结果，agent 自适应换工具；可配置为硬停）。
4. **纳入** 参考接入 `domain/flow/chat`，config 门控、默认开启、可回退。
5. 唯一核心改动：`agent/base.go` 的 `stopReasonForError` 改为识别 `StopReasonError` 接口，使治理类错误产出专属 `StopReason`。此改动落地前先跑 `gitnexus_impact` 评估爆炸半径并向用户报告。

## 目标架构

```
                    ┌─────────────────────────────────────┐
   caller ─────────▶│  harness.Harness (组装器 + Run/Build) │
                    └───────────────┬─────────────────────┘
                                    │ Build()
                    ┌───────────────▼─────────────────────┐
                    │        react.Agent (未改)             │
                    └───┬──────────────┬──────────────┬────┘
       decorated client │        composed hooks       │ decorated tools
   ┌────────────────────▼───┐  ┌───────▼────────┐  ┌───▼─────────────────┐
   │ Replay→Budget→Breaker  │  │ MultiHooks:    │  │ Guard→Timeout→Retry │
   │ →Timeout→Retry→base    │  │ Observability  │  │ →PanicRecover→tool  │
   │ (可选接口逐一转发)       │  │ +Governance    │  └─────────────────────┘
   └────────────────────────┘  │ +用户 hooks     │
                               └────────────────┘
```

- **client 装饰器链**：可靠性（retry/timeout/breaker）+ 治理（budget，看得到 `LLMResult.Usage`）+ 确定性（record/replay）。装饰器实现 `llm.Client`，并**逐一转发**可选升级接口 `ToolCallingClient`/`StreamEventClient`/`ToolStreamEventClient`/`VisionClient`，否则 `AutoPlanner` 的能力探测会误判。
- **tool 装饰器**：`Tool.Invoke` 内层包裹（必须在 `Runner.errorAsOutput` 吞错之前）——retry/timeout/panic 恢复 + 授权门禁。
- **组合 Hooks**：`MultiHooks` 扇出到 可观测 Hooks + 治理 Hooks + 调用方原有 Hooks，按序执行，首个错误终止。

## 核心改动（唯一）：可扩展 StopReason

`agent/base.go`：

```go
// StopReasonError 允许错误自带专属停止原因。
type StopReasonError interface{ AgentStopReason() StopReason }

func stopReasonForError(err error) StopReason {
    var sre StopReasonError
    if errors.As(err, &sre) { return sre.AgentStopReason() }
    if errors.Is(err, ErrMaxIterations) { return StopMaxIterations }
    return StopError
}
```

harness 错误实现该接口，产出 `StopBudgetExceeded` / `StopDeadlineExceeded` / `StopToolDenied`（值为 `agent.StopReason` 类型字符串，无需向 core 增加常量）。

## 包结构（新增 `internal/core/agent/harness/`，业务无关，与 react 并列）

| 文件 | 职责 |
|---|---|
| `harness.go` / `options.go` | `Harness` 组装器与选项；`Build() *react.Agent` + 便捷 `Run` |
| `client_mw.go` | `llm.Client` 装饰器基座 `clientDecorator` + 可选接口转发辅助 |
| `retry.go` | `RetryClient` + `Retryable` 瞬时错误分类、指数退避+jitter |
| `timeout.go` | `TimeoutClient`（单调用 `context.WithTimeout`） |
| `breaker.go` | `BreakerClient`（按 provider 熔断，打开即快速失败） |
| `tool_mw.go` | `resilientTool`：retry/timeout/panic 恢复 + `guardedTool` 授权门禁 |
| `budget.go` | `Budget`（线程安全计数）+ `budgetClient` 装饰器（累计 Usage，越界返错） |
| `cost.go` | `CostModel`（按 model 的 $/1K in+out 折算成本） |
| `policy.go` | `Policy`：工具白名单、最大工具调用数、墙钟 deadline、动态授权器 |
| `hooks_multi.go` | `MultiHooks` 扇出 |
| `hooks_obs.go` | `observabilityHooks`：metrics/trace/结构化日志 |
| `hooks_gov.go` | `governanceHooks`：工具计数 + 墙钟检查 → `StopReasonError` |
| `metrics.go` / `tracer.go` | `Metrics`/`Tracer` 接口 + noop 默认（Prometheus/OTel 适配缝，本次不实现具体后端） |
| `record.go` / `cassette.go` | `recordClient`/`replayClient` + `Cassette`（JSON 磁带，请求指纹匹配） |
| `errors.go` | `ErrBudgetExceeded`/`ErrDeadlineExceeded`/`ErrToolDenied` + `StopReasonError` 实现 |
| `*_test.go` | 各单元测试 |

## 四支柱 → 机制映射

### 可靠性（Resilience）
- **模型**：`RetryClient` 对瞬时错误（超时、429、5xx、连接错误）指数退避+jitter 重试，尊重 `ctx` 取消；`TimeoutClient` 单调用 deadline；`BreakerClient` 按 provider 熔断。三者对 `Invoke`/`InvokeResult`/`InvokeWithTools`/流式方法一致生效（经装饰器基座转发）。
- **工具**：`resilientTool` 仅对标注幂等（`Descriptor.Annotations["idempotent"]==true`）的工具重试；单工具超时；`Invoke` panic → error（不冒泡崩溃）。
- **不在范围**：并行工具执行。当前 ReAct 每轮单 Action，无并行需求（YAGNI），文档显式说明；未来若 planner 产出多工具调用再议。

### 治理（Governance）
- `Budget`：`MaxInputTokens/MaxOutputTokens/MaxTotalTokens/MaxCostUSD/MaxToolCalls/MaxWallClock`，线程安全累计。
- **token/成本**：`budgetClient` 每次调用后累计 `LLMResult.Usage`，按 `CostModel` 折算成本；越界返 `ErrBudgetExceeded`（→`StopBudgetExceeded`）。放在 client 层因为只有此处能看到 `Usage`。
- **工具调用数 / 墙钟**：`governanceHooks` 在 `BeforeTool` 递增计数、在 `BeforeTransition` 检查墙钟 deadline；越界返 `ErrBudgetExceeded`/`ErrDeadlineExceeded`。
- **工具门禁**：静态白名单在建 registry 时过滤（未授权工具不暴露给模型）；可选动态授权器 `guardedTool`，默认**返回拒绝观察结果**（`Output.Text="tool %q is not permitted"`，agent 自适应），可配置 `PolicyMode=HardStop` 改为返回 `ErrToolDenied`（→`StopToolDenied`）。

### 可观测（Observability）
- `observabilityHooks` 发射：
  - counter：`agent_runs_total`、`agent_iterations_total`、`agent_tool_calls_total{tool,status}`、`agent_model_calls_total{status}`、`agent_errors_total{reason}`、`agent_tokens_total{type}`
  - histogram：`agent_run_duration_seconds`、`agent_model_latency_seconds`、`agent_tool_latency_seconds`
- 经 `xlog`（slog）打结构化日志：run 开始/结束/错误、tool 调用、model 调用；自动带 ctx 中 `request_id`/`user_id`（`xlog.With` 已支持）。
- `Tracer` 起 run/model/tool span。`Metrics`/`Tracer` 均为接口 + noop 默认，留 Prometheus/OTel 适配缝。
- 计时状态：`Harness` 每次 `Run` 构造一份新的 `observabilityHooks` 实例持有起始时间戳（Hooks 收到的是只读 State 快照，不能寄存可变状态）。

### 确定性（Determinism）
- 三模式 `Off / Record / Replay`（harness option）。
- `recordClient` 每次调用把 请求指纹（归一化 messages+opts 的哈希）+ `LLMResult` 追加进 `Cassette`（JSON 文件）。
- `replayClient` 按指纹匹配返回录制结果，不走网络；未命中默认严格报错（可配置 passthrough）。
- 任意 provider 可录，供回归与可复现 trace；与现有 `integration/fakeopenai`、`make server-db-smoke` 对接为确定性 LLM。

## 数据流与错误语义

- **预算越界 = 受控终止**：client 装饰器返 `err`，经 planner → 主循环 → `finishWithError`；`result` 仍携带已完成 `Steps`，`StopReason` 区分于真实崩溃，便于指标区分"超支停止 vs 错误"。
- **工具重试位置**：`Runner` 默认 `errorAsOutput=true` 会把工具错误转成观察结果，故重试/超时/panic 恢复必须包在 `Tool.Invoke` 内层（装饰工具本身），不能在 Runner 外层。
- **可选接口转发**：装饰器若不转发 `ToolCallingClient` 等，`AutoPlanner` 会误判模型不支持原生工具调用而退回文本 ReAct。装饰器基座统一按底层是否实现对应接口来暴露。

## 接入 `domain/flow/chat`（第 5 期，config 门控）

- `internal/config` 增 `HarnessConfig`（retry 次数/退避、各类 budget、超时、breaker 阈值、determinism 模式）。
- `svc/ServiceContext` 用配置构造 `*harness.Harness`（装饰 client + policy + hooks），供 chat flow 使用。
- chat flow 由直接 `react.New(...)` 改为 `harness.New(...).Build()`；默认开启，配置可整体回退到裸 `react.Agent`（保证行为可回退）。
- 可观测 metrics/tracer 暂用 noop 适配（具体后端非本次范围），但结构化日志经 `xlog` 立即可见。

## 边界与非目标

- **不改写主循环**：`react/agent.go` 的 `Run()` 逻辑不动；仅 `agent/base.go` 一处 `stopReasonForError` 可扩展化。
- **非目标**：并行工具执行、具体 Prometheus/OTel 导出后端、分布式限流、离线 eval 评分器（后续可在此地基上扩展）。
- **确定性回放**只覆盖 LLM 调用层，不录制工具真实副作用（工具确定性由各自实现负责）。

## 测试策略

- **单元**：fake `llm.Client` 注入瞬时失败验证 `RetryClient`/`BreakerClient`；断言 `StopReason` 验证 budget/门禁；临时 cassette 验证 record/replay；fake `Tool` 验证 `resilientTool` panic 恢复与幂等重试；`MultiHooks` 扇出与错误短路。
- **可选接口转发**：断言装饰后的 client 仍被识别为 `ToolCallingClient`/`StreamEventClient`。
- **集成**：一个 harness 级测试用 `integration/fakeopenai` 注入瞬时故障，端到端证明 retry+breaker+budget+可观测联动；chat flow 接入后用 `make server-db-smoke` 验证真实路径不回归。

## 实施分期（每期独立可落地 + 测试）

0. 骨架：包 + `errors.go` + `metrics.go`/`tracer.go` noop + 核心 `StopReasonError` 缝（先 impact 分析）。
1. 可靠性：client retry/timeout/breaker + tool resilient/panic 恢复。
2. 治理：`Budget`/`CostModel`/`budgetClient` + `Policy`/`guardedTool` + `governanceHooks`。
3. 可观测：`observabilityHooks` + `MultiHooks` + xlog 对接。
4. 确定性：`recordClient`/`replayClient` + `Cassette`。
5. 接入：`HarnessConfig` + `svc` 装配 + chat flow 切换（config 门控、可回退）。
