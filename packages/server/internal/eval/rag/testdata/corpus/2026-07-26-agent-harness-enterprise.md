# 企业级 Agent Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 Cove 服务端的 ReAct Agent 引擎补齐可靠性、治理、可观测、确定性四支柱，作为业务无关的 `internal/core/agent/harness` 包，并接入 chat flow。

**Architecture:** 方案 A —— 用三种横切机制包裹现有 `react.Agent`：(1) `llm.Client` 装饰器链（retry/timeout/breaker/budget/record-replay），(2) `Tool.Invoke` 装饰器（retry/timeout/panic/门禁），(3) 组合 `Hooks`（可观测 + 治理 + 用户）。核心主循环不改，仅 `agent/base.go` 的 `stopReasonForError` 一处改为识别 `StopReasonError` 接口。

**Tech Stack:** Go 1.25，标准库为主（`context`/`time`/`errors`/`log/slog`/`sync`/`crypto/sha256`/`encoding/json`）；复用 `internal/core/llm`、`internal/core/tool`、`internal/core/agent/react`、`internal/observability/xlog`、`integration/fakeopenai`。

## Global Constraints

- Go module 根: `github.com/boxify/api-go`；所有 import 用此前缀。
- 依赖方向由外向内：`core/agent/harness` **不得** 引用 HTTP handler、具体 DB 适配器或 `logic` 层；只依赖 `core/llm`、`core/tool`、`core/agent`、`observability/xlog`。
- 每个 `.go` 文件单一职责；新增文件均带包内 doc 注释，公开类型/函数中文注释与现有 core 风格一致。
- TDD：先写失败测试 → 跑到失败 → 最小实现 → 跑到通过 → 提交。测试用 fake，不依赖真实网络/DB。
- 测试命令统一：`go test ./internal/core/agent/harness/...`（在 `packages/server/` 目录下运行）。
- 提交信息用中文 + emoji 前缀（与仓库既有风格一致，如 `✨ feat(harness): ...`），结尾附 `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`。
- 每改一个已有 symbol 前，先跑 `gitnexus_impact({target, direction:"upstream"})` 并报告爆炸半径；`stopReasonForError` 为唯一核心改动。
- 提交前跑 `gitnexus_detect_changes()` 校验影响范围。

---

## File Structure

新增包 `internal/core/agent/harness/`：

| 文件 | 职责 |
|---|---|
| `doc.go` | 包说明 |
| `errors.go` | `StopReasonError` 接口、`stopError` 帮助、`ErrBudgetExceeded`/`ErrDeadlineExceeded`/`ErrToolDenied` |
| `metrics.go` | `Metrics` 接口 + `NoopMetrics` |
| `tracer.go` | `Tracer`/`Span` 接口 + `NoopTracer` |
| `client_mw.go` | `clientDecorator` 基座 + 可选接口转发（`decorate` 辅助） |
| `retry.go` | `RetryClient` + `Retryable(err) bool` + `RetryConfig` |
| `timeout.go` | `TimeoutClient` + `TimeoutConfig` |
| `breaker.go` | `BreakerClient` + `breaker` 状态机 |
| `budget.go` | `Budget`、`budgetClient`、`BudgetConfig` |
| `cost.go` | `CostModel`、`CostTable` |
| `policy.go` | `Policy`、`PolicyMode`、`Authorizer`、`guardedTool` |
| `tool_mw.go` | `resilientTool` + `ToolResilienceConfig` |
| `hooks_multi.go` | `MultiHooks` |
| `hooks_obs.go` | `observabilityHooks` + `newObservabilityHooks` |
| `hooks_gov.go` | `governanceHooks` |
| `cassette.go` | `Cassette`、`interaction`、指纹 `fingerprint` |
| `record.go` | `recordClient`、`replayClient`、`DeterminismMode` |
| `harness.go` | `Harness`、`New`、`Build`、`Run` |
| `options.go` | `Option` 全套 |

修改既有：
- `internal/core/agent/base.go`：`stopReasonForError` 可扩展化 + 新增 `StopReasonError` 类型（放 `types.go`）。
- `internal/core/agent/types.go`：新增 `StopReasonError` 接口 + 治理停止原因常量。
- Phase 5：`internal/config/*`、`internal/svc/servicecontext.go`、`internal/domain/flow/chat/orchestrator.go`。

---

## Task 0.1: 核心 StopReason 可扩展化（唯一核心改动）

**Files:**
- Modify: `internal/core/agent/types.go`（新增接口 + 常量）
- Modify: `internal/core/agent/base.go:200-205`（`stopReasonForError`）
- Test: `internal/core/agent/base_test.go`（追加用例）

**Interfaces:**
- Produces: `agent.StopReasonError interface { AgentStopReason() StopReason }`；常量 `agent.StopBudgetExceeded`、`agent.StopDeadlineExceeded`、`agent.StopToolDenied`（`StopReason` 类型）。

- [ ] **Step 1: 先跑 impact 分析**

Run: `gitnexus_impact({target: "stopReasonForError", direction: "upstream"})`。预期 upstream 只有 `FinishWithError`（同包）。向用户报告风险等级；若 HIGH/CRITICAL 停下确认。

- [ ] **Step 2: 写失败测试**

在 `internal/core/agent/base_test.go` 追加：

```go
type stopReasonErr struct{ reason StopReason }

func (e stopReasonErr) Error() string            { return "stop: " + string(e.reason) }
func (e stopReasonErr) AgentStopReason() StopReason { return e.reason }

func TestStopReasonForError_CustomInterface(t *testing.T) {
	err := fmt.Errorf("wrap: %w", stopReasonErr{reason: StopBudgetExceeded})
	if got := stopReasonForError(err); got != StopBudgetExceeded {
		t.Fatalf("got %q, want %q", got, StopBudgetExceeded)
	}
}

func TestStopReasonForError_FallbackMaxIterations(t *testing.T) {
	if got := stopReasonForError(ErrMaxIterations); got != StopMaxIterations {
		t.Fatalf("got %q, want %q", got, StopMaxIterations)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/core/agent/ -run TestStopReasonForError`
Expected: 编译失败（`StopBudgetExceeded` 未定义）。

- [ ] **Step 4: 最小实现**

`internal/core/agent/types.go` 新增：

```go
// StopReasonError 允许错误自带专属停止原因，供 harness 等外层扩展停止语义。
type StopReasonError interface {
	AgentStopReason() StopReason
}

const (
	// StopBudgetExceeded 表示触达 token/成本/工具调用预算上限。
	StopBudgetExceeded StopReason = "budget_exceeded"
	// StopDeadlineExceeded 表示触达墙钟时间上限。
	StopDeadlineExceeded StopReason = "deadline_exceeded"
	// StopToolDenied 表示命中未授权工具且策略为硬停。
	StopToolDenied StopReason = "tool_denied"
)
```

`internal/core/agent/base.go` 改 `stopReasonForError`：

```go
func stopReasonForError(err error) StopReason {
	var sre StopReasonError
	if errors.As(err, &sre) {
		return sre.AgentStopReason()
	}
	if errors.Is(err, ErrMaxIterations) {
		return StopMaxIterations
	}
	return StopError
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/core/agent/...`
Expected: PASS（含既有测试不回归）。

- [ ] **Step 6: detect_changes + 提交**

Run: `gitnexus_detect_changes()`，确认仅影响 `stopReasonForError`/新增常量。

```bash
git add internal/core/agent/types.go internal/core/agent/base.go internal/core/agent/base_test.go
git commit -m "✨ feat(agent): stopReasonForError 支持 StopReasonError 扩展停止原因"
```

---

## Task 0.2: harness 包骨架（errors / metrics / tracer / doc）

**Files:**
- Create: `internal/core/agent/harness/doc.go`、`errors.go`、`metrics.go`、`tracer.go`
- Test: `internal/core/agent/harness/errors_test.go`

**Interfaces:**
- Produces:
  - `harness.ErrBudgetExceeded`、`harness.ErrDeadlineExceeded`、`harness.ErrToolDenied`（均实现 `agent.StopReasonError`）
  - `harness.Metrics interface { IncrCounter(name string, labels map[string]string); ObserveHistogram(name string, seconds float64, labels map[string]string) }`；`harness.NoopMetrics`
  - `harness.Tracer interface { StartSpan(ctx, name string) (context.Context, Span) }`；`harness.Span interface { End(err error); SetAttr(k string, v any) }`；`harness.NoopTracer`、`harness.NoopSpan`

- [ ] **Step 1: 写失败测试**

`errors_test.go`：

```go
package harness

import (
	"errors"
	"testing"

	coreagent "github.com/boxify/api-go/internal/core/agent"
)

func TestBudgetError_ImplementsStopReason(t *testing.T) {
	var sre coreagent.StopReasonError
	if !errors.As(ErrBudgetExceeded, &sre) {
		t.Fatal("ErrBudgetExceeded 应实现 StopReasonError")
	}
	if sre.AgentStopReason() != coreagent.StopBudgetExceeded {
		t.Fatalf("got %q", sre.AgentStopReason())
	}
}

func TestToolDeniedError_StopReason(t *testing.T) {
	var sre coreagent.StopReasonError
	if !errors.As(ErrToolDenied, &sre) || sre.AgentStopReason() != coreagent.StopToolDenied {
		t.Fatal("ErrToolDenied 应映射 StopToolDenied")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/core/agent/harness/`
Expected: 编译失败（包不存在）。

- [ ] **Step 3: 最小实现**

`doc.go`：

```go
// Package harness 为 core/agent/react 提供企业级横切能力：可靠性、治理、可观测、确定性。
// 通过装饰 llm.Client、tool.Tool 和组合 agent.Hooks 实现，不修改 react 主循环。
package harness
```

`errors.go`：

```go
package harness

import coreagent "github.com/boxify/api-go/internal/core/agent"

// stopError 把一个静态停止原因附加到 sentinel 错误上。
type stopError struct {
	msg    string
	reason coreagent.StopReason
}

func (e *stopError) Error() string                          { return e.msg }
func (e *stopError) AgentStopReason() coreagent.StopReason  { return e.reason }

var (
	// ErrBudgetExceeded 表示触达 token/成本/工具调用预算。
	ErrBudgetExceeded = &stopError{"harness: budget exceeded", coreagent.StopBudgetExceeded}
	// ErrDeadlineExceeded 表示触达墙钟时间上限。
	ErrDeadlineExceeded = &stopError{"harness: wall-clock deadline exceeded", coreagent.StopDeadlineExceeded}
	// ErrToolDenied 表示命中未授权工具且策略为硬停。
	ErrToolDenied = &stopError{"harness: tool denied by policy", coreagent.StopToolDenied}
)
```

`metrics.go`：

```go
package harness

// Metrics 是可观测计数与直方图的最小抽象，便于对接 Prometheus/OTel。
type Metrics interface {
	IncrCounter(name string, labels map[string]string)
	ObserveHistogram(name string, seconds float64, labels map[string]string)
}

// NoopMetrics 不记录任何指标。
type NoopMetrics struct{}

func (NoopMetrics) IncrCounter(string, map[string]string)               {}
func (NoopMetrics) ObserveHistogram(string, float64, map[string]string) {}
```

`tracer.go`：

```go
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

func (NoopTracer) StartSpan(ctx context.Context, _ string) (context.Context, Span) {
	return ctx, NoopSpan{}
}

// NoopSpan 是空 span。
type NoopSpan struct{}

func (NoopSpan) End(error)            {}
func (NoopSpan) SetAttr(string, any) {}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/core/agent/harness/`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/core/agent/harness/
git commit -m "✨ feat(harness): 包骨架 errors/metrics/tracer 与停止原因映射"
```

---

## Task 1.1: client 装饰器基座 + 可选接口转发

**Files:**
- Create: `internal/core/agent/harness/client_mw.go`
- Test: `internal/core/agent/harness/client_mw_test.go`

**Interfaces:**
- Consumes: `llm.Client` 及可选接口 `llm.ToolCallingClient`/`llm.StreamEventClient`/`llm.ToolStreamEventClient`/`llm.VisionClient`。
- Produces:
  - `type clientMiddleware func(llm.Client) llm.Client`
  - `func chainClient(base llm.Client, mws ...clientMiddleware) llm.Client`
  - 内部 `baseDecorator` 嵌入 `llm.Client`；辅助 `forwardOptional(base, wrapped llm.Client) llm.Client` 依据 base 是否实现可选接口，返回同时满足这些接口的包装体。

**关键约束**：装饰器改写 `Invoke`/`InvokeResult`/`Stream`/`InvokeWithTools`/`StreamEvents`/`StreamWithTools`/`Vision` 的行为时，必须保证 `AutoPlanner` 能通过 `client.(llm.ToolCallingClient)` 等断言探测到底层能力。做法：为每种可选接口组合生成一个包装类型（用 struct 嵌入 + 显式方法），`forwardOptional` 按 base 实现的接口位掩码选择返回类型。

- [ ] **Step 1: 写失败测试**

```go
package harness

import (
	"context"
	"testing"

	"github.com/boxify/api-go/internal/core/llm"
)

type fakeToolCalling struct{ llm.Client }

func (fakeToolCalling) InvokeWithTools(context.Context, []*llm.Message, ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return &llm.LLMResult{Text: "tc"}, nil
}

type baseClient struct{}

func (baseClient) Invoke(context.Context, []*llm.Message, ...llm.ModelCallOption) (string, error) { return "ok", nil }
func (baseClient) InvokeResult(context.Context, []*llm.Message, ...llm.ModelCallOption) (*llm.LLMResult, error) { return &llm.LLMResult{Text: "ok"}, nil }
func (baseClient) Stream(context.Context, []*llm.Message, ...llm.ModelCallOption) (<-chan string, error) { ch := make(chan string); close(ch); return ch, nil }
func (baseClient) Embed(context.Context, []string, int, ...llm.EmbeddingOption) ([][]float64, error) { return nil, nil }
func (baseClient) EmbedOne(context.Context, string, int) ([]float64, error) { return nil, nil }

func TestChainClient_PreservesToolCallingInterface(t *testing.T) {
	base := fakeToolCalling{Client: baseClient{}}
	// 恒等中间件也必须保留可选接口
	wrapped := chainClient(base, func(c llm.Client) llm.Client { return c })
	if _, ok := wrapped.(llm.ToolCallingClient); !ok {
		t.Fatal("链装饰后应仍实现 ToolCallingClient")
	}
}

func TestChainClient_NoOptionalWhenBaseLacksIt(t *testing.T) {
	wrapped := chainClient(baseClient{}, func(c llm.Client) llm.Client { return c })
	if _, ok := wrapped.(llm.ToolCallingClient); ok {
		t.Fatal("base 不支持工具调用时不应虚假暴露 ToolCallingClient")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/core/agent/harness/ -run TestChainClient`
Expected: 编译失败（`chainClient` 未定义）。

- [ ] **Step 3: 最小实现**

`client_mw.go`：核心是 `forwardOptional`。实现方式（避免 2^N 组合爆炸）：定义一个持有 4 个可选接口字段的动态包装，用类型断言在方法内转发；但 Go 静态接口断言需要方法真实存在。采用「按 base 能力选返回具体类型」：为 4 个可选接口分别写 shim，并用组合结构覆盖常见组合。实现代码：

```go
package harness

import "github.com/boxify/api-go/internal/core/llm"

type clientMiddleware func(llm.Client) llm.Client

// chainClient 依次套用中间件（后者在外层），并保留 base 的可选接口能力。
func chainClient(base llm.Client, mws ...clientMiddleware) llm.Client {
	wrapped := base
	for _, mw := range mws {
		if mw != nil {
			wrapped = mw(wrapped)
		}
	}
	return forwardOptional(base, wrapped)
}

// optionalShim 把 core Client 行为委托给 wrapped，把可选接口委托给 base 的原始实现。
// 注意：可选接口（工具调用/流式/vision）本身不经过中间件改写，因 harness 的 retry/budget
// 主要针对 InvokeResult/InvokeWithTools 结果；如需覆盖工具调用重试，见 Task 1.2 说明。
type optionalShim struct {
	llm.Client
	tc  llm.ToolCallingClient
	se  llm.StreamEventClient
	tse llm.ToolStreamEventClient
	vc  llm.VisionClient
}

func (s optionalShim) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return s.tc.InvokeWithTools(ctx, m, o...)
}
func (s optionalShim) StreamEvents(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan llm.StreamEvent, error) {
	return s.se.StreamEvents(ctx, m, o...)
}
func (s optionalShim) StreamWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan llm.StreamEvent, error) {
	return s.tse.StreamWithTools(ctx, m, o...)
}
func (s optionalShim) Vision(ctx context.Context, p, img, mime string, o ...llm.ModelCallOption) (*llm.VisionResult, error) {
	return s.vc.Vision(ctx, p, img, mime, o...)
}
```

**说明给实现者**：Go 中一个 struct 一旦定义了某方法就“永远”实现该接口，无法按实例隐藏。因此 `optionalShim` 单一类型会对不支持的 base 也虚假暴露接口，违反 Task 1.1 第二个测试。正确做法：用一个小的**类型分派表**——按 base 实现了哪几个可选接口，返回一个恰好实现那几个接口的具体类型。为控制组合数，先实现最常见的两种：`ToolCallingClient`（chat agent 必需）与 `ToolCallingClient+StreamEventClient`。其余组合退化为「只暴露 base Client + 命中的单接口」。`forwardOptional` 骨架：

```go
func forwardOptional(base, wrapped llm.Client) llm.Client {
	tc, hasTC := base.(llm.ToolCallingClient)
	se, hasSE := base.(llm.StreamEventClient)
	switch {
	case hasTC && hasSE:
		return tcSeShim{Client: wrapped, tc: tc, se: se}
	case hasTC:
		return tcShim{Client: wrapped, tc: tc}
	case hasSE:
		return seShim{Client: wrapped, se: se}
	default:
		return wrapped
	}
}

type tcShim struct{ llm.Client; tc llm.ToolCallingClient }
func (s tcShim) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) { return s.tc.InvokeWithTools(ctx, m, o...) }

type seShim struct{ llm.Client; se llm.StreamEventClient }
func (s seShim) StreamEvents(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan llm.StreamEvent, error) { return s.se.StreamEvents(ctx, m, o...) }

type tcSeShim struct{ llm.Client; tc llm.ToolCallingClient; se llm.StreamEventClient }
func (s tcSeShim) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) { return s.tc.InvokeWithTools(ctx, m, o...) }
func (s tcSeShim) StreamEvents(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan llm.StreamEvent, error) { return s.se.StreamEvents(ctx, m, o...) }
```

（删除上面示例中的 `optionalShim`，仅保留分派表方案；`import "context"` 补齐。）

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/core/agent/harness/ -run TestChainClient`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/core/agent/harness/client_mw.go internal/core/agent/harness/client_mw_test.go
git commit -m "✨ feat(harness): client 装饰器基座与可选接口转发"
```

---

## Task 1.2: 模型重试 + 指数退避（RetryClient）

**Files:**
- Create: `internal/core/agent/harness/retry.go`
- Test: `internal/core/agent/harness/retry_test.go`

**Interfaces:**
- Consumes: `chainClient`/`clientMiddleware`（Task 1.1）。
- Produces:
  - `type RetryConfig struct { MaxAttempts int; BaseDelay, MaxDelay time.Duration; Retryable func(error) bool }`
  - `func withRetry(cfg RetryConfig) clientMiddleware`
  - `func DefaultRetryable(err error) bool`（超时/`context` 未取消/连接类错误返回 true；`context.Canceled`/`context.DeadlineExceeded` 由 ctx 主导则不重试）

- [ ] **Step 1: 写失败测试**

```go
func TestRetryClient_RetriesTransientThenSucceeds(t *testing.T) {
	var calls int
	base := &scriptedClient{invokeResult: func() (*llm.LLMResult, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("temporary failure")
		}
		return &llm.LLMResult{Text: "ok"}, nil
	}}
	c := chainClient(base, withRetry(RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, Retryable: func(error) bool { return true }}))
	res, err := c.InvokeResult(context.Background(), nil)
	if err != nil || res.Text != "ok" || calls != 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestRetryClient_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	base := &scriptedClient{invokeResult: func() (*llm.LLMResult, error) { return nil, errors.New("x") }}
	c := chainClient(base, withRetry(RetryConfig{MaxAttempts: 5, BaseDelay: time.Second, Retryable: func(error) bool { return true }}))
	if _, err := c.InvokeResult(ctx, nil); err == nil {
		t.Fatal("已取消的 ctx 应尽快返回")
	}
}
```

（`scriptedClient` 是实现 `llm.Client` 的测试桩，字段为可注入的函数；放在 `retry_test.go` 顶部或共享 `testsupport_test.go`。）

- [ ] **Step 2: 跑测试确认失败** — Run: `go test ./internal/core/agent/harness/ -run TestRetryClient`；Expected: 编译失败。

- [ ] **Step 3: 最小实现**

```go
package harness

import (
	"context"
	"errors"
	"time"

	"github.com/boxify/api-go/internal/core/llm"
)

type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Retryable   func(error) bool
}

func withRetry(cfg RetryConfig) clientMiddleware {
	if cfg.MaxAttempts <= 1 {
		return func(c llm.Client) llm.Client { return c }
	}
	if cfg.Retryable == nil {
		cfg.Retryable = DefaultRetryable
	}
	return func(c llm.Client) llm.Client { return &retryClient{Client: c, cfg: cfg} }
}

type retryClient struct {
	llm.Client
	cfg RetryConfig
}

func (r *retryClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	var last error
	for attempt := 1; attempt <= r.cfg.MaxAttempts; attempt++ {
		res, err := r.Client.InvokeResult(ctx, m, o...)
		if err == nil || !r.cfg.Retryable(err) {
			return res, err
		}
		last = err
		if attempt == r.cfg.MaxAttempts {
			break
		}
		if werr := r.wait(ctx, attempt); werr != nil {
			return nil, werr
		}
	}
	return nil, last
}

// Invoke 同理包裹（文本路径）。
func (r *retryClient) Invoke(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (string, error) {
	var last error
	for attempt := 1; attempt <= r.cfg.MaxAttempts; attempt++ {
		s, err := r.Client.Invoke(ctx, m, o...)
		if err == nil || !r.cfg.Retryable(err) {
			return s, err
		}
		last = err
		if attempt == r.cfg.MaxAttempts {
			break
		}
		if werr := r.wait(ctx, attempt); werr != nil {
			return "", werr
		}
	}
	return "", last
}

func (r *retryClient) wait(ctx context.Context, attempt int) error {
	delay := r.cfg.BaseDelay << (attempt - 1)
	if r.cfg.MaxDelay > 0 && delay > r.cfg.MaxDelay {
		delay = r.cfg.MaxDelay
	}
	// 抖动：取 [delay/2, delay]，避免惊群；用确定性偏移（attempt）而非随机，便于测试。
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func DefaultRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true // 具体 provider 错误分类可在此细化（429/5xx/连接）
}
```

**注意**：`retryClient` 也应包裹 `InvokeWithTools`（若 base 支持），但因 Task 1.1 的分派表把可选接口指向 base，工具调用路径不会经过 `retryClient`。为让工具调用也重试，Task 1.2 增补：`withRetry` 返回的中间件同时让 `forwardOptional` 感知——简化做法：`retryClient` 显式实现 `InvokeWithTools`，且 `forwardOptional` 的 shim 改为委托 `wrapped`（而非原 base）当 wrapped 自身实现该可选接口时优先。实现者据此调整 `forwardOptional`：优先 `wrapped.(llm.ToolCallingClient)`，回退 base。补一条测试 `TestRetryClient_RetriesToolCalling`。

- [ ] **Step 4: 跑测试确认通过** — Run: `go test ./internal/core/agent/harness/ -run TestRetry`；Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/core/agent/harness/retry.go internal/core/agent/harness/retry_test.go internal/core/agent/harness/client_mw.go
git commit -m "✨ feat(harness): 模型调用重试与指数退避"
```

---

## Task 1.3: 模型超时（TimeoutClient）

**Files:** Create `timeout.go`；Test `timeout_test.go`
**Interfaces:** `func withTimeout(d time.Duration) clientMiddleware`（d<=0 为恒等）。

- [ ] **Step 1: 失败测试**

```go
func TestTimeoutClient_CancelsSlowCall(t *testing.T) {
	base := &scriptedClient{invokeResult: func() (*llm.LLMResult, error) {
		time.Sleep(50 * time.Millisecond)
		return &llm.LLMResult{Text: "late"}, nil
	}}
	c := chainClient(base, withTimeout(5*time.Millisecond))
	_, err := c.InvokeResult(context.Background(), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}
```

（`scriptedClient.invokeResult` 需接收 ctx 以支持超时；把桩改为 `invokeResult func(context.Context) (*llm.LLMResult, error)` 并在超时测试中 select ctx.Done。）

- [ ] **Step 2: 跑到失败** — `go test ... -run TestTimeoutClient`
- [ ] **Step 3: 实现**

```go
package harness

import (
	"context"
	"time"

	"github.com/boxify/api-go/internal/core/llm"
)

func withTimeout(d time.Duration) clientMiddleware {
	if d <= 0 {
		return func(c llm.Client) llm.Client { return c }
	}
	return func(c llm.Client) llm.Client { return &timeoutClient{Client: c, d: d} }
}

type timeoutClient struct {
	llm.Client
	d time.Duration
}

func (t *timeoutClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	ctx, cancel := context.WithTimeout(ctx, t.d)
	defer cancel()
	return t.Client.InvokeResult(ctx, m, o...)
}

func (t *timeoutClient) Invoke(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, t.d)
	defer cancel()
	return t.Client.Invoke(ctx, m, o...)
}
```

- [ ] **Step 4: 跑到通过** — `go test ... -run TestTimeoutClient`
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): 模型调用超时"`

---

## Task 1.4: 熔断器（BreakerClient）

**Files:** Create `breaker.go`；Test `breaker_test.go`
**Interfaces:** `type BreakerConfig struct { FailureThreshold int; OpenDuration time.Duration }`；`func withBreaker(cfg BreakerConfig) clientMiddleware`；打开时返回 `ErrCircuitOpen`（`Retryable` 视其为不可重试，快速失败）。

- [ ] **Step 1: 失败测试**

```go
func TestBreaker_OpensAfterThreshold(t *testing.T) {
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) { return nil, errors.New("boom") }}
	c := chainClient(base, withBreaker(BreakerConfig{FailureThreshold: 2, OpenDuration: time.Minute}))
	_, _ = c.InvokeResult(context.Background(), nil)
	_, _ = c.InvokeResult(context.Background(), nil)
	_, err := c.InvokeResult(context.Background(), nil) // 第3次应快速失败
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("want ErrCircuitOpen, got %v", err)
	}
}
```

- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现**（`sync.Mutex` 保护计数与打开时间戳；half-open：`OpenDuration` 后放行一次探测，成功清零，失败重新打开）

```go
package harness

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/boxify/api-go/internal/core/llm"
)

var ErrCircuitOpen = errors.New("harness: circuit breaker open")

type BreakerConfig struct {
	FailureThreshold int
	OpenDuration     time.Duration
}

func withBreaker(cfg BreakerConfig) clientMiddleware {
	if cfg.FailureThreshold <= 0 {
		return func(c llm.Client) llm.Client { return c }
	}
	b := &breaker{cfg: cfg}
	return func(c llm.Client) llm.Client { return &breakerClient{Client: c, b: b} }
}

type breaker struct {
	cfg      BreakerConfig
	mu       sync.Mutex
	failures int
	openedAt time.Time
}

func (b *breaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures < b.cfg.FailureThreshold {
		return true
	}
	return now.Sub(b.openedAt) >= b.cfg.OpenDuration // half-open 探测
}

func (b *breaker) record(now time.Time, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil {
		b.failures = 0
		return
	}
	b.failures++
	if b.failures >= b.cfg.FailureThreshold {
		b.openedAt = now
	}
}

type breakerClient struct {
	llm.Client
	b *breaker
}

func (c *breakerClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	if !c.b.allow(time.Now()) {
		return nil, ErrCircuitOpen
	}
	res, err := c.Client.InvokeResult(ctx, m, o...)
	c.b.record(time.Now(), err)
	return res, err
}
```

（`Invoke` 同理；`DefaultRetryable(ErrCircuitOpen)` 返回 false —— 在 `retry.go` 的 `DefaultRetryable` 增补 `if errors.Is(err, ErrCircuitOpen) { return false }`，并补测试。）

- [ ] **Step 4: 跑到通过**
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): 按 provider 的熔断器"`

---

## Task 1.5: 工具装饰器（resilientTool：retry/timeout/panic 恢复）

**Files:** Create `tool_mw.go`；Test `tool_mw_test.go`
**Interfaces:**
- `type ToolResilienceConfig struct { MaxAttempts int; BaseDelay, MaxDelay, Timeout time.Duration }`
- `func wrapTool(t coretool.Tool, cfg ToolResilienceConfig) coretool.Tool`（仅当 `Descriptor.Annotations["idempotent"]==true` 才重试；panic → error；超时用 ctx）
- 幂等判定辅助：`func isIdempotent(d coretool.Descriptor) bool`

- [ ] **Step 1: 失败测试**

```go
func TestResilientTool_RecoversPanic(t *testing.T) {
	base := coretool.NewFuncTool(coretool.Descriptor{Name: "boom"}, func(context.Context, coretool.Input) (coretool.Output, error) {
		panic("kaboom")
	})
	wrapped := wrapTool(base, ToolResilienceConfig{})
	_, err := wrapped.Invoke(context.Background(), coretool.Input{})
	if err == nil {
		t.Fatal("panic 应转成 error 而非崩溃")
	}
}

func TestResilientTool_RetriesIdempotent(t *testing.T) {
	var calls int
	base := coretool.NewFuncTool(
		coretool.Descriptor{Name: "search", Annotations: map[string]any{"idempotent": true}},
		func(context.Context, coretool.Input) (coretool.Output, error) {
			calls++
			if calls < 2 {
				return coretool.Output{}, errors.New("flaky")
			}
			return coretool.Output{Text: "ok"}, nil
		})
	wrapped := wrapTool(base, ToolResilienceConfig{MaxAttempts: 3, BaseDelay: time.Millisecond})
	out, err := wrapped.Invoke(context.Background(), coretool.Input{})
	if err != nil || out.Text != "ok" || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestResilientTool_NoRetryWhenNotIdempotent(t *testing.T) {
	var calls int
	base := coretool.NewFuncTool(coretool.Descriptor{Name: "write"}, func(context.Context, coretool.Input) (coretool.Output, error) {
		calls++
		return coretool.Output{}, errors.New("flaky")
	})
	wrapped := wrapTool(base, ToolResilienceConfig{MaxAttempts: 3, BaseDelay: time.Millisecond})
	_, _ = wrapped.Invoke(context.Background(), coretool.Input{})
	if calls != 1 {
		t.Fatalf("非幂等工具不应重试, calls=%d", calls)
	}
}
```

- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现**

```go
package harness

import (
	"context"
	"errors"
	"fmt"
	"time"

	coretool "github.com/boxify/api-go/internal/core/tool"
)

type ToolResilienceConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Timeout     time.Duration
}

func isIdempotent(d coretool.Descriptor) bool {
	v, _ := d.Annotations["idempotent"].(bool)
	return v
}

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

func (r *resilientTool) Invoke(ctx context.Context, input coretool.Input) (out coretool.Output, err error) {
	d, _ := r.inner.Describe(ctx)
	attempts := 1
	if r.cfg.MaxAttempts > 1 && isIdempotent(d) {
		attempts = r.cfg.MaxAttempts
	}
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
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return coretool.Output{}, ctx.Err()
		case <-t.C:
		}
	}
	return out, err
}

func (r *resilientTool) invokeOnce(ctx context.Context, input coretool.Input) (out coretool.Output, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("tool panic: %v", p)
		}
	}()
	if r.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
	}
	return r.inner.Invoke(ctx, input)
}

var _ = errors.New
```

- [ ] **Step 4: 跑到通过**
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): 工具重试/超时/panic 恢复装饰器"`

---

## Task 2.1: Budget 追踪器 + CostModel

**Files:** Create `budget.go`、`cost.go`；Test `budget_test.go`、`cost_test.go`
**Interfaces:**
- `type Budget struct{ ... }`（私有字段 + `sync.Mutex`）；构造 `func NewBudget(cfg BudgetConfig) *Budget`
- `type BudgetConfig struct { MaxInputTokens, MaxOutputTokens, MaxTotalTokens int64; MaxCostUSD float64; MaxToolCalls int }`
- 方法：`func (b *Budget) AddUsage(u llm.TokenUsage, costUSD float64) error`（越界返 `ErrBudgetExceeded`）、`func (b *Budget) AddToolCall() error`、`func (b *Budget) Snapshot() BudgetSnapshot`
- `type CostModel interface { Cost(model string, u llm.TokenUsage) float64 }`；`type CostTable map[string]ModelRate`；`type ModelRate struct{ InputPer1K, OutputPer1K float64 }`；`func (t CostTable) Cost(model string, u llm.TokenUsage) float64`

- [ ] **Step 1: 失败测试**

```go
func TestBudget_TokensExceeded(t *testing.T) {
	b := NewBudget(BudgetConfig{MaxTotalTokens: 100})
	if err := b.AddUsage(llm.TokenUsage{TotalTokens: 60}, 0); err != nil {
		t.Fatalf("首次不应越界: %v", err)
	}
	if err := b.AddUsage(llm.TokenUsage{TotalTokens: 60}, 0); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("累计越界应返回 ErrBudgetExceeded, got %v", err)
	}
}

func TestBudget_ToolCallsExceeded(t *testing.T) {
	b := NewBudget(BudgetConfig{MaxToolCalls: 1})
	_ = b.AddToolCall()
	if err := b.AddToolCall(); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatal("第2次工具调用应越界")
	}
}

func TestCostTable_Cost(t *testing.T) {
	table := CostTable{"gpt-x": {InputPer1K: 0.01, OutputPer1K: 0.03}}
	got := table.Cost("gpt-x", llm.TokenUsage{InputTokens: 1000, OutputTokens: 1000})
	if got != 0.04 {
		t.Fatalf("want 0.04, got %v", got)
	}
}
```

- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现**

```go
// budget.go
package harness

import (
	"sync"

	"github.com/boxify/api-go/internal/core/llm"
)

type BudgetConfig struct {
	MaxInputTokens  int64
	MaxOutputTokens int64
	MaxTotalTokens  int64
	MaxCostUSD      float64
	MaxToolCalls    int
}

type Budget struct {
	cfg BudgetConfig
	mu  sync.Mutex
	in, out, total int64
	cost           float64
	toolCalls      int
}

func NewBudget(cfg BudgetConfig) *Budget { return &Budget{cfg: cfg} }

func (b *Budget) AddUsage(u llm.TokenUsage, costUSD float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.in += u.InputTokens
	b.out += u.OutputTokens
	b.total += u.TotalTokens
	b.cost += costUSD
	c := b.cfg
	switch {
	case c.MaxInputTokens > 0 && b.in > c.MaxInputTokens,
		c.MaxOutputTokens > 0 && b.out > c.MaxOutputTokens,
		c.MaxTotalTokens > 0 && b.total > c.MaxTotalTokens,
		c.MaxCostUSD > 0 && b.cost > c.MaxCostUSD:
		return ErrBudgetExceeded
	}
	return nil
}

func (b *Budget) AddToolCall() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.toolCalls++
	if b.cfg.MaxToolCalls > 0 && b.toolCalls > b.cfg.MaxToolCalls {
		return ErrBudgetExceeded
	}
	return nil
}

type BudgetSnapshot struct {
	InputTokens, OutputTokens, TotalTokens int64
	CostUSD                                float64
	ToolCalls                              int
}

func (b *Budget) Snapshot() BudgetSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BudgetSnapshot{b.in, b.out, b.total, b.cost, b.toolCalls}
}
```

```go
// cost.go
package harness

import "github.com/boxify/api-go/internal/core/llm"

type ModelRate struct {
	InputPer1K  float64
	OutputPer1K float64
}

type CostModel interface {
	Cost(model string, u llm.TokenUsage) float64
}

type CostTable map[string]ModelRate

func (t CostTable) Cost(model string, u llm.TokenUsage) float64 {
	rate, ok := t[model]
	if !ok {
		return 0
	}
	return float64(u.InputTokens)/1000*rate.InputPer1K + float64(u.OutputTokens)/1000*rate.OutputPer1K
}
```

- [ ] **Step 4: 跑到通过**
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): Budget 追踪器与成本模型"`

---

## Task 2.2: 预算 client 装饰器（budgetClient）

**Files:** append to `budget.go`；Test append `budget_test.go`
**Interfaces:** `func withBudget(b *Budget, cost CostModel) clientMiddleware`（每次 `InvokeResult`/`InvokeWithTools` 成功后 `b.AddUsage(res.Usage, cost.Cost(res.Model, res.Usage))`；越界返 `ErrBudgetExceeded`，此错误经主循环映射为 `StopBudgetExceeded`）。

- [ ] **Step 1: 失败测试**

```go
func TestBudgetClient_StopsWhenExceeded(t *testing.T) {
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) {
		return &llm.LLMResult{Text: "x", Usage: llm.TokenUsage{TotalTokens: 200}}, nil
	}}
	b := NewBudget(BudgetConfig{MaxTotalTokens: 100})
	c := chainClient(base, withBudget(b, CostTable{}))
	if _, err := c.InvokeResult(context.Background(), nil); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("want ErrBudgetExceeded, got %v", err)
	}
}
```

- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现**

```go
func withBudget(b *Budget, cost CostModel) clientMiddleware {
	if b == nil {
		return func(c llm.Client) llm.Client { return c }
	}
	if cost == nil {
		cost = CostTable{}
	}
	return func(c llm.Client) llm.Client { return &budgetClient{Client: c, b: b, cost: cost} }
}

type budgetClient struct {
	llm.Client
	b    *Budget
	cost CostModel
}

func (c *budgetClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	res, err := c.Client.InvokeResult(ctx, m, o...)
	if err != nil {
		return res, err
	}
	if berr := c.b.AddUsage(res.Usage, c.cost.Cost(res.Model, res.Usage)); berr != nil {
		return res, berr
	}
	return res, nil
}
```

（同样包裹 `InvokeWithTools`：因走可选接口，`budgetClient` 显式实现 `InvokeWithTools` 委托底层再计费；`forwardOptional` 已优先 `wrapped`。补测试 `TestBudgetClient_ToolCalling`。）

- [ ] **Step 4: 跑到通过**
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): 预算 client 装饰器（token/成本门禁）"`

---

## Task 2.3: Policy + 工具门禁（guardedTool）

**Files:** Create `policy.go`；Test `policy_test.go`
**Interfaces:**
- `type PolicyMode int`（`PolicyRefuse`=0 默认 / `PolicyHardStop`）
- `type Authorizer func(ctx context.Context, tool string, input coretool.Input) bool`
- `type Policy struct { Allowlist []string; Mode PolicyMode; Authorize Authorizer }`；`func (p Policy) allowed(name string) bool`
- `func guardTool(t coretool.Tool, p Policy) coretool.Tool`：未授权时——`PolicyRefuse` 返回 `Output{Text: "tool %q is not permitted"}` 且 err=nil；`PolicyHardStop` 返回 `ErrToolDenied`。
- 白名单过滤在建 registry 时用；`Authorize` 为运行期动态门禁。

- [ ] **Step 1: 失败测试**

```go
func TestGuardTool_RefuseByDefault(t *testing.T) {
	base := coretool.NewFuncTool(coretool.Descriptor{Name: "danger"}, func(context.Context, coretool.Input) (coretool.Output, error) {
		return coretool.Output{Text: "did danger"}, nil
	})
	g := guardTool(base, Policy{Authorize: func(context.Context, string, coretool.Input) bool { return false }})
	out, err := g.Invoke(context.Background(), coretool.Input{})
	if err != nil {
		t.Fatalf("默认拒绝应返回观察结果而非错误: %v", err)
	}
	if out.Text == "did danger" {
		t.Fatal("未授权工具不应真正执行")
	}
}

func TestGuardTool_HardStop(t *testing.T) {
	base := coretool.NewFuncTool(coretool.Descriptor{Name: "danger"}, func(context.Context, coretool.Input) (coretool.Output, error) { return coretool.Output{}, nil })
	g := guardTool(base, Policy{Mode: PolicyHardStop, Authorize: func(context.Context, string, coretool.Input) bool { return false }})
	if _, err := g.Invoke(context.Background(), coretool.Input{}); !errors.Is(err, ErrToolDenied) {
		t.Fatalf("硬停模式应返回 ErrToolDenied, got %v", err)
	}
}
```

- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现**

```go
package harness

import (
	"context"
	"fmt"

	coretool "github.com/boxify/api-go/internal/core/tool"
)

type PolicyMode int

const (
	PolicyRefuse PolicyMode = iota
	PolicyHardStop
)

type Authorizer func(ctx context.Context, tool string, input coretool.Input) bool

type Policy struct {
	Allowlist []string
	Mode      PolicyMode
	Authorize Authorizer
}

func (p Policy) allowed(name string) bool {
	if len(p.Allowlist) == 0 {
		return true
	}
	for _, a := range p.Allowlist {
		if a == name {
			return true
		}
	}
	return false
}

func guardTool(t coretool.Tool, p Policy) coretool.Tool {
	if p.Authorize == nil {
		return t
	}
	return &guardedTool{inner: t, policy: p}
}

type guardedTool struct {
	inner  coretool.Tool
	policy Policy
}

func (g *guardedTool) Describe(ctx context.Context) (coretool.Descriptor, error) {
	return g.inner.Describe(ctx)
}

func (g *guardedTool) Invoke(ctx context.Context, input coretool.Input) (coretool.Output, error) {
	d, _ := g.inner.Describe(ctx)
	if g.policy.Authorize(ctx, d.Name, input) {
		return g.inner.Invoke(ctx, input)
	}
	if g.policy.Mode == PolicyHardStop {
		return coretool.Output{}, ErrToolDenied
	}
	return coretool.Output{Text: fmt.Sprintf("tool %q is not permitted by policy", d.Name)}, nil
}
```

- [ ] **Step 4: 跑到通过**
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): 工具授权门禁与白名单策略"`

---

## Task 3.1: MultiHooks 扇出

**Files:** Create `hooks_multi.go`；Test `hooks_multi_test.go`
**Interfaces:** `func MultiHooks(hooks ...react.Hooks) react.Hooks`（按序调用每个 hook 的同名方法，首个非 nil error 短路返回；nil 成员跳过）。`react.Hooks = agent.Hooks[react.Decision, react.Step]`。

- [ ] **Step 1: 失败测试**

```go
package harness

import (
	"context"
	"errors"
	"testing"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
)

type recordHook struct {
	corereact.NoopHooks
	beforeRun *int
	failErr   error
}

func (h recordHook) BeforeRun(ctx context.Context, s corereact.State) error {
	if h.beforeRun != nil {
		*h.beforeRun++
	}
	return h.failErr
}

func TestMultiHooks_FanOut(t *testing.T) {
	var a, b int
	m := MultiHooks(recordHook{beforeRun: &a}, recordHook{beforeRun: &b})
	if err := m.BeforeRun(context.Background(), corereact.State{}); err != nil {
		t.Fatal(err)
	}
	if a != 1 || b != 1 {
		t.Fatalf("a=%d b=%d", a, b)
	}
}

func TestMultiHooks_ShortCircuit(t *testing.T) {
	var b int
	sentinel := errors.New("stop")
	m := MultiHooks(recordHook{failErr: sentinel}, recordHook{beforeRun: &b})
	if err := m.BeforeRun(context.Background(), corereact.State{}); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if b != 0 {
		t.Fatal("首个 hook 出错后不应继续")
	}
}
```

- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现**（`multiHooks []react.Hooks` 实现全部 12 个方法，逐个转发；返回类方法遇 error 短路。为避免样板过多，仍需显式写出每个方法——这是 Go 无 mixin 的代价。）

```go
package harness

import (
	"context"

	coreagent "github.com/boxify/api-go/internal/core/agent"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/llm"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

func MultiHooks(hooks ...corereact.Hooks) corereact.Hooks {
	out := make([]corereact.Hooks, 0, len(hooks))
	for _, h := range hooks {
		if h != nil {
			out = append(out, h)
		}
	}
	return multiHooks(out)
}

type multiHooks []corereact.Hooks

func (m multiHooks) BeforeRun(ctx context.Context, s corereact.State) error {
	for _, h := range m {
		if err := h.BeforeRun(ctx, s); err != nil {
			return err
		}
	}
	return nil
}
// ...（其余 11 个方法同构：AfterRun/BeforeTransition/AfterTransition/BeforeModel/OnToken/
// AfterModel/AfterParse/BeforeTool/AfterTool/OnStep/OnError —— 逐个 for-range 转发并短路。
// 签名严格对齐 Task 参考：
//   AfterRun(ctx, result corereact.Result, runErr error) error
//   BeforeTransition(ctx, s corereact.State, tr corereact.Transition) error
//   BeforeModel(ctx, s corereact.State, msgs []*llm.Message) error
//   OnToken(ctx, s corereact.State, text string) error
//   AfterModel(ctx, s corereact.State, output string, modelErr error) error
//   AfterParse(ctx, s corereact.State, d corereact.Decision, parseErr error) error
//   BeforeTool(ctx, s corereact.State, call corereact.ToolCall) error
//   AfterTool(ctx, s corereact.State, call corereact.ToolCall, out coretool.Output, toolErr error) error
//   OnStep(ctx, s corereact.State, step corereact.Step) error
//   OnError(ctx, s corereact.State, err error) error
// )
var (
	_ = coreagent.StopError
	_ = coretool.Output{}
	_ = llm.TokenUsage{}
)
```

**实现者须补全全部 12 个方法**（上面注释列了准确签名）。

- [ ] **Step 4: 跑到通过**
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): MultiHooks 生命周期扇出"`

---

## Task 3.2: 治理 Hooks（工具计数 + 墙钟）

**Files:** Create `hooks_gov.go`；Test `hooks_gov_test.go`
**Interfaces:** `func newGovernanceHooks(b *Budget, deadline time.Time) corereact.Hooks`。`BeforeTool` → `b.AddToolCall()`（越界返 `ErrBudgetExceeded`）；`BeforeTransition` → 若 `!deadline.IsZero() && time.Now().After(deadline)` 返 `ErrDeadlineExceeded`。其余方法 noop（嵌入 `corereact.NoopHooks`）。

- [ ] **Step 1: 失败测试**

```go
func TestGovernanceHooks_ToolCallBudget(t *testing.T) {
	b := NewBudget(BudgetConfig{MaxToolCalls: 1})
	h := newGovernanceHooks(b, time.Time{})
	if err := h.BeforeTool(context.Background(), corereact.State{}, corereact.ToolCall{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := h.BeforeTool(context.Background(), corereact.State{}, corereact.ToolCall{Name: "x"}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("第2次工具调用应越界, got %v", err)
	}
}

func TestGovernanceHooks_Deadline(t *testing.T) {
	h := newGovernanceHooks(NewBudget(BudgetConfig{}), time.Now().Add(-time.Second))
	if err := h.BeforeTransition(context.Background(), corereact.State{}, corereact.Transition{}); !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("已过 deadline 应返回 ErrDeadlineExceeded, got %v", err)
	}
}
```

- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现**

```go
package harness

import (
	"context"
	"time"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
)

func newGovernanceHooks(b *Budget, deadline time.Time) corereact.Hooks {
	return &governanceHooks{budget: b, deadline: deadline}
}

type governanceHooks struct {
	corereact.NoopHooks
	budget   *Budget
	deadline time.Time
}

func (h *governanceHooks) BeforeTool(ctx context.Context, _ corereact.State, _ corereact.ToolCall) error {
	if h.budget == nil {
		return nil
	}
	return h.budget.AddToolCall()
}

func (h *governanceHooks) BeforeTransition(ctx context.Context, _ corereact.State, _ corereact.Transition) error {
	if !h.deadline.IsZero() && time.Now().After(h.deadline) {
		return ErrDeadlineExceeded
	}
	return nil
}
```

- [ ] **Step 4: 跑到通过**
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): 治理 Hooks（工具计数与墙钟）"`

---

## Task 3.3: 可观测 Hooks（metrics/trace/xlog）

**Files:** Create `hooks_obs.go`；Test `hooks_obs_test.go`
**Interfaces:** `func newObservabilityHooks(m Metrics, tr Tracer) corereact.Hooks`（每次 Run 由 Harness 新建实例；内部 `sync.Mutex` + 起始时间戳 map 记录时延）。发射 Task 规格中的 counter/histogram；`AfterRun`/`OnError` 打 `xlog` 结构化日志。

- [ ] **Step 1: 失败测试**

```go
type spyMetrics struct {
	mu       sync.Mutex
	counters map[string]int
}

func (s *spyMetrics) IncrCounter(name string, _ map[string]string) {
	s.mu.Lock(); defer s.mu.Unlock()
	if s.counters == nil { s.counters = map[string]int{} }
	s.counters[name]++
}
func (s *spyMetrics) ObserveHistogram(string, float64, map[string]string) {}

func TestObservabilityHooks_CountsToolCalls(t *testing.T) {
	m := &spyMetrics{}
	h := newObservabilityHooks(m, NoopTracer{})
	call := corereact.ToolCall{Name: "search"}
	_ = h.BeforeTool(context.Background(), corereact.State{}, call)
	_ = h.AfterTool(context.Background(), corereact.State{}, call, coretool.Output{}, nil)
	if m.counters["agent_tool_calls_total"] == 0 {
		t.Fatal("应发射 agent_tool_calls_total")
	}
}
```

- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现**（`BeforeRun` 记 run 起始时间；`AfterRun` 观察 `agent_run_duration_seconds` 并 `IncrCounter("agent_runs_total")`；`BeforeModel`/`AfterModel` 记 model 时延与 `agent_model_calls_total{status}`；`BeforeTool`/`AfterTool` 记 tool 时延与 `agent_tool_calls_total{tool,status}`；`OnError` 记 `agent_errors_total{reason}` 并 `xlog` 打错误日志。用 `xlog` 从 ctx 取 attrs：`slog.Default()` 记录时 ctx 中 request_id/user_id 已由上层注入，直接 `slog.InfoContext(ctx, ...)`。时延存储用 `map[string]time.Time` + mutex，键区分 run/model/tool+iteration。）

代码骨架（实现者补全各方法）：

```go
package harness

import (
	"context"
	"log/slog"
	"sync"
	"time"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

func newObservabilityHooks(m Metrics, tr Tracer) corereact.Hooks {
	if m == nil { m = NoopMetrics{} }
	if tr == nil { tr = NoopTracer{} }
	return &observabilityHooks{metrics: m, tracer: tr, starts: map[string]time.Time{}}
}

type observabilityHooks struct {
	corereact.NoopHooks
	metrics Metrics
	tracer  Tracer
	mu      sync.Mutex
	starts  map[string]time.Time
}

func (h *observabilityHooks) mark(key string)           { h.mu.Lock(); h.starts[key] = time.Now(); h.mu.Unlock() }
func (h *observabilityHooks) since(key string) float64  {
	h.mu.Lock(); defer h.mu.Unlock()
	t, ok := h.starts[key]; if !ok { return 0 }
	return time.Since(t).Seconds()
}

func (h *observabilityHooks) BeforeRun(ctx context.Context, _ corereact.State) error { h.mark("run"); return nil }
func (h *observabilityHooks) AfterRun(ctx context.Context, result corereact.Result, runErr error) error {
	h.metrics.IncrCounter("agent_runs_total", nil)
	h.metrics.ObserveHistogram("agent_run_duration_seconds", h.since("run"), nil)
	slog.InfoContext(ctx, "agent run finished", "stopped_by", string(result.StoppedBy), "iterations", result.Iterations, "error", runErr)
	return nil
}
func (h *observabilityHooks) BeforeTool(ctx context.Context, _ corereact.State, call corereact.ToolCall) error {
	h.mark("tool:" + call.Name); return nil
}
func (h *observabilityHooks) AfterTool(ctx context.Context, _ corereact.State, call corereact.ToolCall, _ coretool.Output, toolErr error) error {
	status := "ok"; if toolErr != nil { status = "error" }
	h.metrics.IncrCounter("agent_tool_calls_total", map[string]string{"tool": call.Name, "status": status})
	h.metrics.ObserveHistogram("agent_tool_latency_seconds", h.since("tool:"+call.Name), map[string]string{"tool": call.Name})
	return nil
}
func (h *observabilityHooks) OnError(ctx context.Context, _ corereact.State, err error) error {
	h.metrics.IncrCounter("agent_errors_total", nil)
	slog.ErrorContext(ctx, "agent run error", "error", err)
	return nil
}
```

（补 `BeforeModel`/`AfterModel` 的 model 时延与 `agent_model_calls_total`；补 `OnStep` 的 `agent_iterations_total`。）

- [ ] **Step 4: 跑到通过**
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): 可观测 Hooks（metrics/trace/结构化日志）"`

---

## Task 4.1: Cassette（磁带 + 请求指纹）

**Files:** Create `cassette.go`；Test `cassette_test.go`
**Interfaces:**
- `type interaction struct { Fingerprint string; Result *llm.LLMResult }`
- `type Cassette struct { Interactions []interaction }`
- `func fingerprint(messages []*llm.Message, opts ...llm.ModelCallOption) string`（SHA256 of 归一化 JSON——messages 的 role+content；opts 暂不入指纹以稳健，或仅纳入可序列化子集。先只用 messages）
- `func LoadCassette(path string) (*Cassette, error)`、`func (c *Cassette) Save(path string) error`、`func (c *Cassette) Find(fp string) (*llm.LLMResult, bool)`、`func (c *Cassette) Append(fp string, r *llm.LLMResult)`

- [ ] **Step 1: 失败测试**

```go
func TestCassette_SaveLoadFind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	c := &Cassette{}
	fp := fingerprint([]*llm.Message{{Role: "user", Content: "hi"}})
	c.Append(fp, &llm.LLMResult{Text: "hello"})
	if err := c.Save(path); err != nil { t.Fatal(err) }
	loaded, err := LoadCassette(path)
	if err != nil { t.Fatal(err) }
	got, ok := loaded.Find(fp)
	if !ok || got.Text != "hello" {
		t.Fatalf("ok=%v got=%v", ok, got)
	}
}

func TestFingerprint_Deterministic(t *testing.T) {
	m := []*llm.Message{{Role: "user", Content: "hi"}}
	if fingerprint(m) != fingerprint(m) {
		t.Fatal("指纹应确定性")
	}
}
```

（`llm.Message` 字段名以 `internal/core/llm/message.go` 为准；实现前先读该文件确认 `Role`/`Content` 实际字段。）

- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现**（`fingerprint` 用 `json.Marshal` 归一化 + `sha256.Sum256` → hex；`Save`/`Load` 用 `os.WriteFile`/`os.ReadFile` + `json.Marshal/Unmarshal`；`Find` 线性查找。）

- [ ] **Step 4: 跑到通过**
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): 确定性磁带与请求指纹"`

---

## Task 4.2: record/replay client

**Files:** Create `record.go`；Test `record_test.go`
**Interfaces:**
- `type DeterminismMode int`（`DeterminismOff`=0 / `DeterminismRecord` / `DeterminismReplay`）
- `func withRecord(c *Cassette) clientMiddleware`、`func withReplay(c *Cassette, strict bool) clientMiddleware`
- replay 命中返回录制结果；未命中 strict 返回 `ErrReplayMiss`，否则透传底层。

- [ ] **Step 1: 失败测试**

```go
func TestReplayClient_ReturnsRecorded(t *testing.T) {
	c := &Cassette{}
	fp := fingerprint([]*llm.Message{{Role: "user", Content: "hi"}})
	c.Append(fp, &llm.LLMResult{Text: "recorded"})
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) {
		t.Fatal("replay 命中时不应调用底层"); return nil, nil
	}}
	client := chainClient(base, withReplay(c, true))
	res, err := client.InvokeResult(context.Background(), []*llm.Message{{Role: "user", Content: "hi"}})
	if err != nil || res.Text != "recorded" {
		t.Fatalf("err=%v res=%v", err, res)
	}
}

func TestRecordClient_AppendsInteraction(t *testing.T) {
	c := &Cassette{}
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) {
		return &llm.LLMResult{Text: "live"}, nil
	}}
	client := chainClient(base, withRecord(c))
	_, _ = client.InvokeResult(context.Background(), []*llm.Message{{Role: "user", Content: "hi"}})
	if len(c.Interactions) != 1 {
		t.Fatalf("应录制 1 条, got %d", len(c.Interactions))
	}
}
```

- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现**（`recordClient.InvokeResult` 调底层后 `c.Append(fingerprint(m), res)`；`replayClient.InvokeResult` 先 `c.Find(fingerprint(m))`，命中直接返回，未命中按 strict 决定。定义 `var ErrReplayMiss = errors.New("harness: replay cassette miss")`。）

- [ ] **Step 4: 跑到通过**
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): record/replay client 装饰器"`

---

## Task 5.1: Harness 组装器 + Options

**Files:** Create `harness.go`、`options.go`；Test `harness_test.go`
**Interfaces:**
- `type Harness struct { ... }`
- `func New(client llm.Client, registry *coretool.Registry, opts ...Option) *Harness`
- `func (h *Harness) Build(runOpts ...corereact.Option) *corereact.Agent`（装配装饰 client + 组合 hooks + 装饰工具后的 registry，返回即用 Agent）
- `func (h *Harness) Run(ctx, input corereact.Input, runOpts ...corereact.RunOption) (*corereact.Result, error)`（内部每次新建 per-run Budget + 可观测 hooks，再 Build().Run()）
- Options：`WithRetry`、`WithTimeout`、`WithBreaker`、`WithToolResilience`、`WithBudgetConfig`、`WithCostModel`、`WithPolicy`、`WithWallClock(d)`、`WithMetrics`、`WithTracer`、`WithDeterminism(mode, cassettePath)`、`WithUserHooks(corereact.Hooks)`、`WithSystemPrompt`、`WithModelOptions`。

**关键组装顺序**（client 链，从内到外）：`base → retry → timeout → breaker → budget → replay/record`。工具：先 `guardTool`（门禁）再 `wrapTool`（可靠性），重建一个新 `*coretool.Registry` 注册装饰后的工具。hooks：`MultiHooks(observability, governance, userHooks)`。

- [ ] **Step 1: 失败测试**（端到端：注入瞬时失败的 fake client 经 harness 后成功；预算越界得到 `StopBudgetExceeded`）

```go
func TestHarness_RunRetriesAndSucceeds(t *testing.T) {
	var calls int
	base := &toolCallingFake{invokeWithTools: func() (*llm.LLMResult, error) {
		calls++
		if calls < 2 { return nil, errors.New("flaky") }
		return &llm.LLMResult{Text: "final answer: done", StopReason: "stop"}, nil
	}}
	reg := coretool.NewRegistry()
	h := New(base, reg,
		WithRetry(RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, Retryable: func(error) bool { return true }}),
	)
	res, err := h.Run(context.Background(), corereact.Input{Query: "hi"})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.StoppedBy != corereact.StopFinalAnswer {
		t.Fatalf("stopped=%v", res.StoppedBy)
	}
}
```

（`toolCallingFake` 实现 `llm.Client`+`llm.ToolCallingClient`，返回一个直接 final 的结果，使 ReAct 一轮结束。实现者按 react 文本协议或 function-calling 路径构造最简结果。）

- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现**（按上面组装顺序拼装。`Run` 内：`budget := NewBudget(cfg)`；`obs := newObservabilityHooks(metrics, tracer)`；`gov := newGovernanceHooks(budget, deadline)`；`hooks := MultiHooks(obs, gov, userHooks)`；client 链 `chainClient(base, mws...)`；工具重建 registry；`corereact.New(client, decoratedReg, corereact.WithHooks(hooks), ...opts).Run(ctx, input, runOpts...)`。）

- [ ] **Step 4: 跑到通过**
- [ ] **Step 5: 提交** — `git commit -m "✨ feat(harness): Harness 组装器与选项"`

---

## Task 5.2: 集成测试（fakeopenai 注入故障）

**Files:** Create `internal/core/agent/harness/integration_test.go`（或复用 `integration/`）；先读 `integration/fakeopenai/main.go` 了解可注入行为。
**Interfaces:** 无新公开 API；用真实 `fakeopenai` provider 起一个可注入瞬时 5xx 的 server，经 harness retry 后成功，并断言可观测计数器被触达。

- [ ] **Step 1: 写集成测试**（用 `httptest` 起 fakeopenai，前 N 次返回 503，之后正常；用 spyMetrics 断言 `agent_runs_total>0`、`agent_model_calls_total{status=ok}>0`）
- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 按需在 fakeopenai 增可注入故障开关**（若尚无）
- [ ] **Step 4: 跑到通过** — `go test ./internal/core/agent/harness/ -run TestHarnessIntegration`
- [ ] **Step 5: 提交** — `git commit -m "✅ test(harness): fakeopenai 故障注入集成测试"`

---

## Task 6.1: 服务端配置与接入 chat flow（config 门控）

**Files:**
- Modify: `internal/config/*`（新增 `HarnessConfig` 字段，先 `Read` 现有 config 结构与 yaml）
- Modify: `internal/svc/servicecontext.go`（构造 `*harness.Harness` 或暴露构造参数）
- Modify: `internal/domain/flow/chat/orchestrator.go:180-189`
- Test: `internal/domain/flow/chat/orchestrator_test.go`（补开关回退用例）

**Interfaces:**
- `config.HarnessConfig{ Enabled bool; RetryMaxAttempts int; RetryBaseMs, TimeoutMs, BreakerOpenMs int; BreakerFailures int; MaxTotalTokens int64; MaxToolCalls int; WallClockMs int; Determinism string; CassettePath string }`
- orchestrator 改为：`Enabled` 时 `harness.New(client, registry, mapConfig(cfg)...).Run(...)`；否则保留原 `corereact.New(...).Run(...)`。现有 `agentHooks` 作为 `WithUserHooks` 传入。

- [ ] **Step 0: impact 分析** — `gitnexus_impact({target: "NewOrchestrator", direction: "upstream"})` 与 `runChat`（承载 react.New 的函数）；报告风险。
- [ ] **Step 1: 失败测试**（配置 `Enabled=false` 时行为与旧路径一致；`Enabled=true` 时 run 成功且经过 harness——可用 spyMetrics 注入验证计数器被触达）
- [ ] **Step 2: 跑到失败**
- [ ] **Step 3: 实现 config 字段 + 映射函数 + orchestrator 分支**（保持默认 `Enabled=true`，但配置缺省安全值；`mapConfig` 把 ms→`time.Duration`）
- [ ] **Step 4: 跑到通过** — `go test ./internal/domain/flow/chat/...`
- [ ] **Step 5: detect_changes + 提交** — `git commit -m "✨ feat(chat): 接入企业级 Harness（config 门控可回退）"`

- [ ] **Step 6: 全量校验** — 在 `packages/server/` 跑 `go build ./...` 与 `go test ./...`；跑 `make docs`（若接口/配置产物需同步）；`gitnexus_detect_changes()` 复核。

---

## Self-Review（作者已核对）

**Spec 覆盖**：
- 可扩展 StopReason → Task 0.1 ✓
- 包骨架/errors/metrics/tracer → 0.2 ✓
- client 装饰器 + 可选接口转发 → 1.1 ✓
- retry/timeout/breaker → 1.2/1.3/1.4 ✓
- 工具 resilient/panic → 1.5 ✓
- Budget/CostModel/budgetClient → 2.1/2.2 ✓
- Policy/门禁 → 2.3 ✓
- MultiHooks/governance/observability → 3.1/3.2/3.3 ✓
- Cassette/record-replay → 4.1/4.2 ✓
- Harness 组装 + 集成 → 5.1/5.2 ✓
- 接入 chat flow（config 门控） → 6.1 ✓
- 非目标（并行工具/具体 OTel 后端）：未建任务，符合 spec ✓

**类型一致性**：`clientMiddleware`/`chainClient`/`forwardOptional` 全程一致；`Budget` 方法名 `AddUsage`/`AddToolCall`/`Snapshot` 跨 2.1/2.2/3.2 一致；`corereact.Hooks` 签名以 Task 3.1 注释为准。

**已知实现注意点（非占位，是给实现者的真实约束）**：
- Task 1.1 的可选接口分派表只覆盖 `{}/TC/SE/TC+SE`；若未来需要 vision/tool-stream 组合，按同构补 shim。
- Task 1.2/2.2 需让 `forwardOptional` 优先返回 `wrapped` 自身实现的可选接口（使工具调用路径也经过 retry/budget）——已在 1.2 Step 3 说明。
- Task 4.1 实现前必须 `Read internal/core/llm/message.go` 确认 `Message` 字段名。
- Task 6.1 实现前必须 `Read` 现有 `internal/config` 与 `orchestrator.go` 完整上下文。
