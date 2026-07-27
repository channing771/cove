# Agent 评估体系 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建业务无关的 `internal/eval` 包,用数据集在 harness 包裹的 agent 上跑用例、用三族打分器打分、产出 Go 原生报告并对比基线做回归门禁。

**Architecture:** 四层正交解耦——数据集(`Dataset`/`Case`)→ 运行器(`Runner`/`HarnessRunner`)→ 打分器(`Scorer` 三族)→ 报告(`Report`/`Diff`/门禁)。运行器持有底层 client/registry/harness 选项,内部包一层"仅暴露非流式能力的 usage 装饰器"捕获 token/成本,并可用 harness cassette 回放做到 hermetic。

**Tech Stack:** Go(module `github.com/boxify/api-go`);复用 `internal/core/agent/react`、`internal/core/agent/harness`、`internal/core/llm`、`internal/core/tool`;标准库 `encoding/json`、`regexp`、`text/template`、`testing`。

## Global Constraints

- 模块路径 `github.com/boxify/api-go`;新包位于 `internal/eval` 与 `internal/eval/scorers`。
- 所有导出标识符写**中文 doc 注释**,风格对齐 `internal/core/agent/harness`。
- **TDD**:每个行为先写失败测试再实现;频繁提交。
- Go 二进制在非交互 shell 里用绝对路径:`/Users/chen/.gvm/gos/go1.26.0/bin/go`(下文以 `GO` 代指)。所有命令在 `packages/server` 目录下执行。
- 提交信息结尾附:`Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`。
- **usage 捕获限制**:token/成本仅在**非流式**模型路径(`InvokeResult`/`InvokeWithTools`)可得;`StreamEvent` 不带 usage。eval 装饰器故意只暴露非流式能力,使 eval 走非流式;流式 usage 留二期(与 harness `gen_ai.usage.*` 现状一致)。纯文本 ReAct(走 `Invoke`)无 usage,`TokenBudget`/`CostBudget` 对其判为跳过。
- **打分约定**:scorer 在 `Case.Expect` 缺对应键时返回 `Score{Skipped:true}`(不计入通过/失败);判定错误(如 judge 解析失败)返回 `Score{Err:...}`,该用例计失败。
- **不做**:JSON schema 校验(仅 `JSONValid`)、并行用例调度、Langfuse push、流式 usage——均属二期,YAGNI。

---

### Task 1: 数据集载入(`Case` / `Dataset` / `LoadDataset`)

**Files:**
- Create: `internal/eval/case.go`
- Test: `internal/eval/case_test.go`
- Create: `internal/eval/testdata/datasets/smoke.json`

**Interfaces:**
- Produces:
  - `type Case struct { ID string; Query string; Messages []*llm.Message; Tags []string; Expect map[string]any; Cassette string }`(JSON tag:`id/query/messages/tags/expect/cassette`)
  - `type Dataset struct { Name string; Cases []Case }`(JSON tag:`name/cases`)
  - `func LoadDataset(path string) (*Dataset, error)` —— 读 JSON、校验 ID 非空且唯一、把非空相对 `Cassette` 解析为绝对路径(相对数据集文件所在目录)。

- [ ] **Step 1: 写失败测试**

`internal/eval/case_test.go`:
```go
package eval

import (
	"path/filepath"
	"testing"
)

func TestLoadDataset(t *testing.T) {
	ds, err := LoadDataset("testdata/datasets/smoke.json")
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if ds.Name != "smoke" {
		t.Fatalf("name = %q, want smoke", ds.Name)
	}
	if len(ds.Cases) != 1 {
		t.Fatalf("cases = %d, want 1", len(ds.Cases))
	}
	c := ds.Cases[0]
	if c.ID != "greet-basic" {
		t.Fatalf("id = %q", c.ID)
	}
	if got := c.Expect["stop_reason"]; got != "final_answer" {
		t.Fatalf("expect.stop_reason = %v", got)
	}
	if !filepath.IsAbs(c.Cassette) {
		t.Fatalf("cassette not absolute: %q", c.Cassette)
	}
}

func TestLoadDatasetRejectsDuplicateID(t *testing.T) {
	_, err := LoadDataset("testdata/datasets/dup.json")
	if err == nil {
		t.Fatal("want error for duplicate id")
	}
}
```

`internal/eval/testdata/datasets/smoke.json`:
```json
{
  "name": "smoke",
  "cases": [
    {
      "id": "greet-basic",
      "query": "用一句话解释什么是向量数据库",
      "tags": ["knowledge"],
      "expect": {
        "contains": ["向量"],
        "max_iterations": 2,
        "stop_reason": "final_answer",
        "latency_ms": 60000
      },
      "cassette": "../cassettes/greet-basic.json"
    }
  ]
}
```

`internal/eval/testdata/datasets/dup.json`:
```json
{ "name": "dup", "cases": [ { "id": "x", "query": "a" }, { "id": "x", "query": "b" } ] }
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GO test ./internal/eval/ -run TestLoadDataset -v`
Expected: 编译失败 `undefined: LoadDataset`。

- [ ] **Step 3: 写实现**

`internal/eval/case.go`:
```go
// Package eval 提供业务无关的 Agent 评估体系:数据集、运行器、打分器与报告。
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/boxify/api-go/internal/core/llm"
)

// Case 表示一条评估用例。
//
// Expect 为自由期望,由各打分器各取所需(如 reference 答案、应调工具、预算阈值)。
// Cassette 非空时按相对数据集文件目录解析为绝对路径,供运行器回放。
type Case struct {
	ID       string         `json:"id"`
	Query    string         `json:"query"`
	Messages []*llm.Message `json:"messages,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
	Expect   map[string]any `json:"expect,omitempty"`
	Cassette string         `json:"cassette,omitempty"`
}

// Dataset 表示一组评估用例。
type Dataset struct {
	Name  string `json:"name"`
	Cases []Case `json:"cases"`
}

// LoadDataset 从 JSON 文件载入数据集,校验用例 ID 非空且唯一,并把非空相对
// Cassette 路径解析为相对该文件目录的绝对路径。
func LoadDataset(path string) (*Dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	var ds Dataset
	if err := json.Unmarshal(raw, &ds); err != nil {
		return nil, fmt.Errorf("parse dataset: %w", err)
	}
	dir := filepath.Dir(path)
	seen := make(map[string]bool, len(ds.Cases))
	for i := range ds.Cases {
		c := &ds.Cases[i]
		if c.ID == "" {
			return nil, fmt.Errorf("case %d: empty id", i)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
		if c.Cassette != "" && !filepath.IsAbs(c.Cassette) {
			c.Cassette = filepath.Join(dir, c.Cassette)
		}
	}
	return &ds, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `GO test ./internal/eval/ -run TestLoadDataset -v` 与 `-run TestLoadDatasetRejectsDuplicateID`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/eval/case.go internal/eval/case_test.go internal/eval/testdata/datasets/
git commit -m "✨ feat(eval): 数据集 Case/Dataset 与 LoadDataset（ID 唯一校验 + cassette 路径解析）

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: 打分结果与接口(`Score` / `Scorer` / expect 取值助手)

**Files:**
- Create: `internal/eval/score.go`
- Test: `internal/eval/score_test.go`

**Interfaces:**
- Consumes: `Case`(Task 1)。
- Produces:
  - `type Score struct { Scorer string; Value float64; Passed bool; Skipped bool; Detail string; ErrText string; Err error }`(`Err` 带 `json:"-"`,其余有 JSON tag)。
  - `type Scorer interface { Name() string; Score(ctx context.Context, c Case, r RunRecord) Score }`(`RunRecord` 在 Task 3 定义;本 Task 只需接口引用,故 Task 3 必须存在同包类型——见下方 Step 3 注释)。
  - 取值助手(供 scorers 复用):
    - `func ExpectString(c Case, key string) (string, bool)`
    - `func ExpectStrings(c Case, key string) ([]string, bool)` —— 支持 `string` 或 `[]any` of string。
    - `func ExpectFloat(c Case, key string) (float64, bool)` —— 接受 JSON number(`float64`)。
    - `func ExpectInt(c Case, key string) (int, bool)`
  - 构造助手:`func skip(name string) Score`、`func fail(name, detail string) Score`、`func pass(name string, value float64, detail string) Score`、`func errScore(name string, err error) Score`。

> 注:`Scorer.Score` 引用 `RunRecord`,而 `RunRecord` 在 Task 3 落地。为让本 Task 可独立编译,**先在 Task 3 之前**不引用 `RunRecord` 的字段,仅声明接口。实际执行顺序保证 Task 2 与 Task 3 都完成后才有消费方(Task 5+)。若按顺序执行,Task 2 的 `score.go` 引用 `RunRecord` 类型名会编译失败——因此本 Task 的编译验证放宽为"随 Task 3 一起编译通过";Task 2 单测只测 expect 助手(不涉及 `RunRecord`)。

- [ ] **Step 1: 写失败测试**

`internal/eval/score_test.go`:
```go
package eval

import "testing"

func TestExpectStrings(t *testing.T) {
	c := Case{Expect: map[string]any{
		"one":  "hello",
		"many": []any{"a", "b"},
	}}
	if got, ok := ExpectStrings(c, "one"); !ok || len(got) != 1 || got[0] != "hello" {
		t.Fatalf("one = %v %v", got, ok)
	}
	if got, ok := ExpectStrings(c, "many"); !ok || len(got) != 2 {
		t.Fatalf("many = %v %v", got, ok)
	}
	if _, ok := ExpectStrings(c, "absent"); ok {
		t.Fatal("absent should be false")
	}
}

func TestExpectFloatAndInt(t *testing.T) {
	c := Case{Expect: map[string]any{"n": float64(3)}}
	if f, ok := ExpectFloat(c, "n"); !ok || f != 3 {
		t.Fatalf("float = %v %v", f, ok)
	}
	if n, ok := ExpectInt(c, "n"); !ok || n != 3 {
		t.Fatalf("int = %v %v", n, ok)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GO test ./internal/eval/ -run TestExpect -v`
Expected: 编译失败 `undefined: ExpectStrings`(此时 `score.go` 尚未引用 `RunRecord`,可独立编译)。

- [ ] **Step 3: 写实现**

`internal/eval/score.go`:
```go
package eval

import "context"

// Score 表示单个打分器对一条用例的评分结果。
//
// Value 归一化到 0..1。Skipped 表示该用例缺少对应期望而跳过(不计通过/失败)。
// Err 非 nil 表示打分过程本身出错,该用例应计失败;ErrText 是其可序列化镜像。
type Score struct {
	Scorer  string  `json:"scorer"`
	Value   float64 `json:"value"`
	Passed  bool    `json:"passed"`
	Skipped bool    `json:"skipped,omitempty"`
	Detail  string  `json:"detail,omitempty"`
	ErrText string  `json:"error,omitempty"`
	Err     error   `json:"-"`
}

// Scorer 是无状态、可组合的打分器。一期(离线)与二期(在线)共用本接口。
type Scorer interface {
	Name() string
	Score(ctx context.Context, c Case, r RunRecord) Score
}

func skip(name string) Score { return Score{Scorer: name, Skipped: true, Passed: true, Detail: "skipped: no expectation"} }
func pass(name string, value float64, detail string) Score {
	return Score{Scorer: name, Value: value, Passed: true, Detail: detail}
}
func fail(name, detail string) Score { return Score{Scorer: name, Value: 0, Passed: false, Detail: detail} }
func errScore(name string, err error) Score {
	return Score{Scorer: name, Passed: false, Err: err, ErrText: err.Error()}
}

// ExpectString 读取 Expect[key] 的字符串值。
func ExpectString(c Case, key string) (string, bool) {
	v, ok := c.Expect[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// ExpectStrings 读取 Expect[key],支持单个字符串或字符串数组。
func ExpectStrings(c Case, key string) ([]string, bool) {
	v, ok := c.Expect[key]
	if !ok {
		return nil, false
	}
	switch t := v.(type) {
	case string:
		return []string{t}, true
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	case []string:
		return t, true
	}
	return nil, false
}

// ExpectFloat 读取 Expect[key] 的数值(JSON number 反序列化为 float64)。
func ExpectFloat(c Case, key string) (float64, bool) {
	v, ok := c.Expect[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	}
	return 0, false
}

// ExpectInt 读取 Expect[key] 的整数值。
func ExpectInt(c Case, key string) (int, bool) {
	f, ok := ExpectFloat(c, key)
	return int(f), ok
}

var _ = context.Background // score.go 引用 context 以对齐 Scorer 签名依赖
```

> 说明:`var _ = context.Background` 仅为占位避免未使用 import;实现时若 `context` 已被 `Scorer` 接口签名使用则删除该行。

- [ ] **Step 4: 跑测试确认通过**(与 Task 3 合并编译;若单独执行,先完成 Task 3 再回跑)

Run: `GO test ./internal/eval/ -run TestExpect -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/eval/score.go internal/eval/score_test.go
git commit -m "✨ feat(eval): Score/Scorer 接口与 Expect 取值助手

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: 运行记录与 usage 捕获(`RunRecord` / `Usage` / `wrapUsage`)

**Files:**
- Create: `internal/eval/record.go`
- Test: `internal/eval/record_test.go`

**Interfaces:**
- Consumes: `react.Result`、`llm.Client`/`llm.LLMResult`/`llm.TokenUsage`、`harness.CostModel`。
- Produces:
  - `type Usage struct { InputTokens, OutputTokens, TotalTokens int64; CostUSD float64 }`(全 JSON tag)。
  - `type RunRecord struct { Result *react.Result; Latency time.Duration; Usage Usage; Err error }`。
  - `func (r RunRecord) Answer() string`、`func (r RunRecord) Iterations() int`、`func (r RunRecord) StopReason() string`、`func (r RunRecord) ToolTrajectory() []string` —— 均对 `Result==nil` 安全。
  - `func wrapUsage(base llm.Client, cost harness.CostModel) (llm.Client, *Usage)` —— 返回**仅暴露非流式能力**的装饰器:恒包 `InvokeResult`;base 实现 `llm.ToolCallingClient` 时额外包 `InvokeWithTools`;两者都在返回后累加 `LLMResult.Usage` 与 `cost.Cost(model,usage)` 到 `*Usage`。故意不暴露 Stream/Vision,使 eval 走非流式。

- [ ] **Step 1: 写失败测试**

`internal/eval/record_test.go`:
```go
package eval

import (
	"context"
	"testing"

	coreagent "github.com/boxify/api-go/internal/core/agent"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/agent/harness"
	"github.com/boxify/api-go/internal/core/llm"
)

func TestToolTrajectory(t *testing.T) {
	r := RunRecord{Result: &corereact.Result{
		Steps: []corereact.Step{
			{Action: "search"},
			{Action: ""}, // 终局步无 Action
			{Action: "calc"},
		},
		StoppedBy: coreagent.StopFinalAnswer,
	}}
	got := r.ToolTrajectory()
	if len(got) != 2 || got[0] != "search" || got[1] != "calc" {
		t.Fatalf("trajectory = %v", got)
	}
	if r.StopReason() != "final_answer" {
		t.Fatalf("stop = %q", r.StopReason())
	}
}

func TestToolTrajectoryNilSafe(t *testing.T) {
	var r RunRecord
	if r.ToolTrajectory() != nil || r.Answer() != "" || r.Iterations() != 0 || r.StopReason() != "" {
		t.Fatal("nil result accessors must be zero-valued")
	}
}

func TestWrapUsageCapturesToolCalling(t *testing.T) {
	base := &fakeToolClient{usage: llm.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, model: "m"}
	cost := harness.CostTable{"m": {Input: 0.001, Output: 0.002}} // 见下方 CostTable 结构说明
	wc, u := wrapUsage(base, cost)
	tc, ok := wc.(llm.ToolCallingClient)
	if !ok {
		t.Fatal("wrapped tool client must expose ToolCallingClient")
	}
	if _, err := tc.InvokeWithTools(context.Background(), nil); err != nil {
		t.Fatalf("InvokeWithTools: %v", err)
	}
	if u.TotalTokens != 15 || u.InputTokens != 10 {
		t.Fatalf("usage = %+v", *u)
	}
	if u.CostUSD <= 0 {
		t.Fatalf("cost not accumulated: %v", u.CostUSD)
	}
}

func TestWrapUsageTextClientHidesToolCalling(t *testing.T) {
	base := &fakeTextClient{}
	wc, _ := wrapUsage(base, nil)
	if _, ok := wc.(llm.ToolCallingClient); ok {
		t.Fatal("text client must NOT expose ToolCallingClient")
	}
}
```

> **CostTable 结构核对**:实现前先 `Read internal/core/agent/harness/cost.go` 确认 `CostTable` 的键/值类型与字段名(input/output 单价字段名),按真实结构改上面的 `harness.CostTable{...}` 字面量与 Task 6/10 中的用法。

- [ ] **Step 2: 加测试用 fake client**

在 `internal/eval/testsupport_test.go`(新建)放共享 fake:
```go
package eval

import (
	"context"
	"errors"

	"github.com/boxify/api-go/internal/core/llm"
)

// fakeTextClient 只实现 llm.Client 核心方法(纯文本 ReAct 路径)。
type fakeTextClient struct {
	outputs []string
	usage   llm.TokenUsage
	model   string
}

func (f *fakeTextClient) Invoke(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (string, error) {
	if len(f.outputs) == 0 {
		return "", errors.New("no output")
	}
	out := f.outputs[0]
	f.outputs = f.outputs[1:]
	return out, nil
}
func (f *fakeTextClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	txt, err := f.Invoke(ctx, m, o...)
	if err != nil {
		return nil, err
	}
	return &llm.LLMResult{Text: txt, Usage: f.usage, Model: f.model}, nil
}
func (f *fakeTextClient) Stream(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan string, error) {
	return nil, errors.New("no stream")
}
func (f *fakeTextClient) Embed(ctx context.Context, texts []string, dim int, o ...llm.EmbeddingOption) ([][]float64, error) {
	return nil, errors.New("no embed")
}
func (f *fakeTextClient) EmbedOne(ctx context.Context, text string, dim int) ([]float64, error) {
	return nil, errors.New("no embed")
}

// fakeToolClient 额外实现 llm.ToolCallingClient(非流式工具调用路径,携带 usage)。
type fakeToolClient struct {
	fakeTextClient
	results []*llm.LLMResult
	usage   llm.TokenUsage
	model   string
}

func (f *fakeToolClient) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	if len(f.results) > 0 {
		r := f.results[0]
		f.results = f.results[1:]
		return r, nil
	}
	// 默认:无工具调用 = 终局答案,携带 usage。
	return &llm.LLMResult{Text: "final answer", Usage: f.usage, Model: f.model}, nil
}
```

> 注:`fakeToolClient` 嵌入 `fakeTextClient` 又各自带 `usage/model` 字段会遮蔽;实现时把 `usage/model` 只留在 `fakeTextClient`,`fakeToolClient` 通过 `f.fakeTextClient.usage` 使用,或在构造时同时设置。执行者按编译器提示消歧即可。

- [ ] **Step 3: 跑测试确认失败**

Run: `GO test ./internal/eval/ -run 'TestToolTrajectory|TestWrapUsage' -v`
Expected: 编译失败 `undefined: RunRecord`/`wrapUsage`。

- [ ] **Step 4: 写实现**

`internal/eval/record.go`:
```go
package eval

import (
	"context"
	"time"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/agent/harness"
	"github.com/boxify/api-go/internal/core/llm"
)

// Usage 表示一次运行累计的 token 与折算成本。
type Usage struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// RunRecord 表示 SUT 跑完一条用例的产物。所有访问器对 Result==nil 安全。
type RunRecord struct {
	Result  *corereact.Result
	Latency time.Duration
	Usage   Usage
	Err     error
}

// Answer 返回最终答案;Result 为 nil 时返回空串。
func (r RunRecord) Answer() string {
	if r.Result == nil {
		return ""
	}
	return r.Result.Answer
}

// Iterations 返回迭代次数;Result 为 nil 时返回 0。
func (r RunRecord) Iterations() int {
	if r.Result == nil {
		return 0
	}
	return r.Result.Iterations
}

// StopReason 返回停止原因字符串;Result 为 nil 时返回空串。
func (r RunRecord) StopReason() string {
	if r.Result == nil {
		return ""
	}
	return string(r.Result.StoppedBy)
}

// ToolTrajectory 返回按序调用的工具名(跳过无 Action 的终局步)。
func (r RunRecord) ToolTrajectory() []string {
	if r.Result == nil {
		return nil
	}
	var out []string
	for _, s := range r.Result.Steps {
		if s.Action != "" {
			out = append(out, s.Action)
		}
	}
	return out
}

// usageClient 装饰非流式结构化生成路径,累加 token 与成本。仅暴露 llm.Client 核心能力。
type usageClient struct {
	llm.Client
	usage *Usage
	cost  harness.CostModel
}

func (c *usageClient) add(res *llm.LLMResult) {
	if res == nil {
		return
	}
	u := res.Usage
	c.usage.InputTokens += u.InputTokens
	c.usage.OutputTokens += u.OutputTokens
	c.usage.TotalTokens += u.TotalTokens
	if c.cost != nil {
		c.usage.CostUSD += c.cost.Cost(res.Model, u)
	}
}

func (c *usageClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	res, err := c.Client.InvokeResult(ctx, m, o...)
	if err != nil {
		return res, err
	}
	c.add(res)
	return res, nil
}

// usageToolClient 额外装饰非流式原生工具调用路径。
type usageToolClient struct {
	*usageClient
	base llm.ToolCallingClient
}

func (c *usageToolClient) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	res, err := c.base.InvokeWithTools(ctx, m, o...)
	if err != nil {
		return res, err
	}
	c.add(res)
	return res, nil
}

// wrapUsage 返回仅暴露非流式能力的 usage 捕获装饰器与其累加指针。
//
// base 实现 llm.ToolCallingClient 时,返回值同样实现之(经 InvokeWithTools 捕获);
// 否则只暴露 llm.Client 核心方法。故意不暴露 Stream/Vision,使 eval 运行走非流式,
// 从而可捕获 token/成本(StreamEvent 不带 usage)。
func wrapUsage(base llm.Client, cost harness.CostModel) (llm.Client, *Usage) {
	u := &Usage{}
	uc := &usageClient{Client: base, usage: u, cost: cost}
	if tc, ok := base.(llm.ToolCallingClient); ok {
		return &usageToolClient{usageClient: uc, base: tc}, u
	}
	return uc, u
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `GO test ./internal/eval/ -run 'TestToolTrajectory|TestWrapUsage' -v`
Expected: PASS(四个测试全绿)。

- [ ] **Step 6: 提交**

```bash
git add internal/eval/record.go internal/eval/record_test.go internal/eval/testsupport_test.go
git commit -m "✨ feat(eval): RunRecord 访问器 + 仅暴露非流式能力的 usage 捕获装饰器

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: 运行器(`Runner` / `HarnessRunner`)

**Files:**
- Create: `internal/eval/runner.go`
- Test: `internal/eval/runner_test.go`

**Interfaces:**
- Consumes: `Case`(T1)、`RunRecord`/`wrapUsage`(T3)、`harness.New`/`harness.Option`/`harness.WithCostModel`/`harness.WithDeterminism`/`harness.DeterminismReplay`、`react.Input`、`llm.Client`、`coretool.Registry`。
- Produces:
  - `type Runner interface { Run(ctx context.Context, c Case) (RunRecord, error) }`
  - `type HarnessRunner struct { Client llm.Client; Registry *coretool.Registry; Cost harness.CostModel; Options []harness.Option }`
    - 约束:`Options` **不得**含 `WithCostModel`/`WithDeterminism`(运行器内部注入)。
  - `func (r *HarnessRunner) Run(ctx context.Context, c Case) (RunRecord, error)` —— 包 usage → 组装 options → 计时跑 harness → 组 `RunRecord`。agent 运行错误落入 `RunRecord.Err`(不作为 Run 的返回 error);仅基础设施失败(如构造)才返回 error。

- [ ] **Step 1: 写失败测试**

`internal/eval/runner_test.go`:
```go
package eval

import (
	"context"
	"testing"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
)

func TestHarnessRunnerToolCallingCapturesUsage(t *testing.T) {
	client := &fakeToolClient{model: "m"}
	client.fakeTextClient.usage = mustUsage(12, 8) // 见 testsupport helper
	r := &HarnessRunner{Client: client}
	rec, err := r.Run(context.Background(), Case{ID: "c1", Query: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Answer() != "final answer" {
		t.Fatalf("answer = %q", rec.Answer())
	}
	if rec.StopReason() != string(corereact.StopFinalAnswer) {
		t.Fatalf("stop = %q", rec.StopReason())
	}
	if rec.Iterations() != 1 {
		t.Fatalf("iters = %d", rec.Iterations())
	}
	if rec.Latency <= 0 {
		t.Fatal("latency must be > 0")
	}
	if rec.Usage.TotalTokens != 20 {
		t.Fatalf("total tokens = %d, want 20", rec.Usage.TotalTokens)
	}
}
```

在 `testsupport_test.go` 增补:
```go
func mustUsage(in, out int64) llm.TokenUsage {
	return llm.TokenUsage{InputTokens: in, OutputTokens: out, TotalTokens: in + out}
}
```

> 若 `fakeToolClient` 的 usage 字段消歧后不叫 `fakeTextClient.usage`,按 Task 3 实际实现调整该赋值。

- [ ] **Step 2: 跑测试确认失败**

Run: `GO test ./internal/eval/ -run TestHarnessRunner -v`
Expected: 编译失败 `undefined: HarnessRunner`。

- [ ] **Step 3: 写实现**

`internal/eval/runner.go`:
```go
package eval

import (
	"context"
	"time"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/agent/harness"
	"github.com/boxify/api-go/internal/core/llm"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

// Runner 是被评估对象(SUT)的适配器:把一条用例跑成一条运行记录。
type Runner interface {
	Run(ctx context.Context, c Case) (RunRecord, error)
}

// HarnessRunner 用 harness 包裹的 agent 作为 SUT。
//
// Options 为静态 harness 选项(budget/policy/prompt/maxIterations 等),不得包含
// WithCostModel/WithDeterminism——运行器内部注入 usage 捕获与(按 Case.Cassette)回放。
type HarnessRunner struct {
	Client   llm.Client
	Registry *coretool.Registry
	Cost     harness.CostModel
	Options  []harness.Option
}

// Run 跑一条用例:注入 usage 捕获与可选回放,计时执行,组装 RunRecord。
// agent 运行错误落入 RunRecord.Err;返回的 error 仅用于基础设施级失败。
func (r *HarnessRunner) Run(ctx context.Context, c Case) (RunRecord, error) {
	client, usage := wrapUsage(r.Client, r.Cost)

	opts := make([]harness.Option, 0, len(r.Options)+2)
	opts = append(opts, r.Options...)
	opts = append(opts, harness.WithCostModel(r.Cost))
	if c.Cassette != "" {
		opts = append(opts, harness.WithDeterminism(harness.DeterminismReplay, c.Cassette))
	}

	h := harness.New(client, r.Registry, opts...)
	start := time.Now()
	res, runErr := h.Run(ctx, corereact.Input{Query: c.Query, Messages: c.Messages})
	latency := time.Since(start)

	return RunRecord{Result: res, Latency: latency, Usage: *usage, Err: runErr}, nil
}

var _ Runner = (*HarnessRunner)(nil)
```

> **核对点**:确认 `harness.WithCostModel(nil)` 安全(cost 为 nil 时 harness 内部退化为 `CostTable{}`——见 budget.go)。此处即便 `r.Cost==nil` 也传 `WithCostModel(nil)`,harness 会兜底;usage 装饰器侧 `cost==nil` 时不累加 CostUSD。两处一致。

- [ ] **Step 4: 跑测试确认通过**

Run: `GO test ./internal/eval/ -run TestHarnessRunner -v`
Expected: PASS。若 `Iterations()!=1` 或未捕获 usage,核对 fakeToolClient 是否走了 `InvokeWithTools`(而非 Stream);usage 装饰器不暴露 Stream,应强制非流式。

- [ ] **Step 5: 提交**

```bash
git add internal/eval/runner.go internal/eval/runner_test.go internal/eval/testsupport_test.go
git commit -m "✨ feat(eval): HarnessRunner —— usage 注入 + 可选 cassette 回放 + 计时

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: 确定性打分器(`scorers/deterministic.go`)

**Files:**
- Create: `internal/eval/scorers/deterministic.go`
- Test: `internal/eval/scorers/deterministic_test.go`
- Create: `internal/eval/scorers/doc.go`

**Interfaces:**
- Consumes: `eval.Case`、`eval.RunRecord`、`eval.Scorer`、`eval` 的 `Expect*` 助手。
- Produces(均返回 `eval.Scorer`):
  - `func ExactMatch() eval.Scorer`(key `answer`)
  - `func Contains() eval.Scorer`(key `contains`,全含才过)
  - `func Regex() eval.Scorer`(key `regex`)
  - `func JSONValid() eval.Scorer`(无 key;`Answer` 是否合法 JSON——恒执行)
  - `func ToolTrajectory() eval.Scorer`(key `tools`:`{"mode":"contains|exact","names":[...]}` 或直接 `[...]` 视为 contains)
  - `func StopReasonIs() eval.Scorer`(key `stop_reason`)
  - `func MaxIterations() eval.Scorer`(key `max_iterations`)

> 因 scorers 是独立包,`eval` 包的 `pass/fail/skip/errScore` 是**非导出**的。方案:在 `eval` 包新增**导出**构造器 `NewScore`,或让 scorers 自行构造 `eval.Score{...}`。**采用后者**:scorers 直接返回 `eval.Score{Scorer:..., Passed:..., Value:..., Skipped:..., Detail:...}` 字面量,不依赖非导出助手。判错用 `eval.Score{Scorer:..., Err:err, ErrText:err.Error()}`。

- [ ] **Step 1: 写失败测试**

`internal/eval/scorers/deterministic_test.go`:
```go
package scorers

import (
	"context"
	"testing"

	coreagent "github.com/boxify/api-go/internal/core/agent"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/eval"
)

func rec(answer string, steps ...string) eval.RunRecord {
	rr := &corereact.Result{Answer: answer, Iterations: len(steps) + 1, StoppedBy: coreagent.StopFinalAnswer}
	for _, s := range steps {
		rr.Steps = append(rr.Steps, corereact.Step{Action: s})
	}
	return eval.RunRecord{Result: rr}
}

func TestContains(t *testing.T) {
	s := Contains()
	got := s.Score(context.Background(), eval.Case{Expect: map[string]any{"contains": []any{"向量"}}}, rec("向量数据库存的是向量"))
	if !got.Passed {
		t.Fatalf("want pass, got %+v", got)
	}
	miss := s.Score(context.Background(), eval.Case{Expect: map[string]any{"contains": "缺失"}}, rec("无关内容"))
	if miss.Passed {
		t.Fatal("want fail")
	}
	sk := s.Score(context.Background(), eval.Case{Expect: map[string]any{}}, rec("x"))
	if !sk.Skipped {
		t.Fatal("want skipped when no expectation")
	}
}

func TestToolTrajectoryContainsAndExact(t *testing.T) {
	s := ToolTrajectory()
	c := eval.Case{Expect: map[string]any{"tools": map[string]any{"mode": "exact", "names": []any{"search", "calc"}}}}
	if !s.Score(context.Background(), c, rec("a", "search", "calc")).Passed {
		t.Fatal("exact should pass")
	}
	if s.Score(context.Background(), c, rec("a", "search")).Passed {
		t.Fatal("exact mismatch should fail")
	}
}

func TestStopReasonAndMaxIterations(t *testing.T) {
	if !StopReasonIs().Score(context.Background(), eval.Case{Expect: map[string]any{"stop_reason": "final_answer"}}, rec("x")).Passed {
		t.Fatal("stop reason should pass")
	}
	if MaxIterations().Score(context.Background(), eval.Case{Expect: map[string]any{"max_iterations": float64(1)}}, rec("x", "t1")).Passed {
		t.Fatal("2 iters > max 1 should fail")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GO test ./internal/eval/scorers/ -run 'TestContains|TestToolTrajectory|TestStopReason' -v`
Expected: 编译失败 `undefined: Contains`。

- [ ] **Step 3: 写实现**

`internal/eval/scorers/doc.go`:
```go
// Package scorers 提供 eval 的内置打分器:确定性断言、性能/成本、LLM-as-judge。
package scorers
```

`internal/eval/scorers/deterministic.go`:
```go
package scorers

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/boxify/api-go/internal/eval"
)

func skipped(name string) eval.Score {
	return eval.Score{Scorer: name, Skipped: true, Passed: true, Detail: "skipped: no expectation"}
}
func passed(name string, detail string) eval.Score {
	return eval.Score{Scorer: name, Value: 1, Passed: true, Detail: detail}
}
func failed(name, detail string) eval.Score {
	return eval.Score{Scorer: name, Value: 0, Passed: false, Detail: detail}
}

type fn struct {
	name string
	f    func(ctx context.Context, c eval.Case, r eval.RunRecord) eval.Score
}

func (s fn) Name() string { return s.name }
func (s fn) Score(ctx context.Context, c eval.Case, r eval.RunRecord) eval.Score {
	return s.f(ctx, c, r)
}

// ExactMatch 断言最终答案与 expect.answer 全等(去首尾空白)。
func ExactMatch() eval.Scorer {
	return fn{"exact_match", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		want, ok := eval.ExpectString(c, "answer")
		if !ok {
			return skipped("exact_match")
		}
		if strings.TrimSpace(r.Answer()) == strings.TrimSpace(want) {
			return passed("exact_match", "")
		}
		return failed("exact_match", fmt.Sprintf("answer %q != %q", r.Answer(), want))
	}}
}

// Contains 断言最终答案包含 expect.contains 的所有子串。
func Contains() eval.Scorer {
	return fn{"contains", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		subs, ok := eval.ExpectStrings(c, "contains")
		if !ok {
			return skipped("contains")
		}
		for _, sub := range subs {
			if !strings.Contains(r.Answer(), sub) {
				return failed("contains", fmt.Sprintf("missing %q", sub))
			}
		}
		return passed("contains", "")
	}}
}

// Regex 断言最终答案匹配 expect.regex。
func Regex() eval.Scorer {
	return fn{"regex", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		pat, ok := eval.ExpectString(c, "regex")
		if !ok {
			return skipped("regex")
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return eval.Score{Scorer: "regex", Err: err, ErrText: err.Error()}
		}
		if re.MatchString(r.Answer()) {
			return passed("regex", "")
		}
		return failed("regex", "no match")
	}}
}

// JSONValid 断言最终答案是合法 JSON(恒执行,无对应 expect key)。
func JSONValid() eval.Scorer {
	return fn{"json_valid", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		if _, ok := c.Expect["json_valid"]; !ok {
			return skipped("json_valid")
		}
		var v any
		if err := json.Unmarshal([]byte(r.Answer()), &v); err != nil {
			return failed("json_valid", err.Error())
		}
		return passed("json_valid", "")
	}}
}

// ToolTrajectory 断言工具调用轨迹满足 expect.tools。
//
// tools 可为字符串数组(默认 contains 子集模式),或对象 {"mode":"contains|exact","names":[...]}。
func ToolTrajectory() eval.Scorer {
	return fn{"tool_trajectory", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		raw, ok := c.Expect["tools"]
		if !ok {
			return skipped("tool_trajectory")
		}
		mode, names := "contains", []string{}
		switch t := raw.(type) {
		case map[string]any:
			if m, ok := t["mode"].(string); ok {
				mode = m
			}
			if arr, ok := t["names"].([]any); ok {
				for _, e := range arr {
					if s, ok := e.(string); ok {
						names = append(names, s)
					}
				}
			}
		case []any:
			for _, e := range t {
				if s, ok := e.(string); ok {
					names = append(names, s)
				}
			}
		default:
			return eval.Score{Scorer: "tool_trajectory", Err: fmt.Errorf("bad tools expectation"), ErrText: "bad tools expectation"}
		}
		got := r.ToolTrajectory()
		if mode == "exact" {
			if len(got) != len(names) {
				return failed("tool_trajectory", fmt.Sprintf("got %v want exact %v", got, names))
			}
			for i := range names {
				if got[i] != names[i] {
					return failed("tool_trajectory", fmt.Sprintf("got %v want exact %v", got, names))
				}
			}
			return passed("tool_trajectory", "")
		}
		set := map[string]bool{}
		for _, g := range got {
			set[g] = true
		}
		for _, n := range names {
			if !set[n] {
				return failed("tool_trajectory", fmt.Sprintf("missing tool %q in %v", n, got))
			}
		}
		return passed("tool_trajectory", "")
	}}
}

// StopReasonIs 断言停止原因等于 expect.stop_reason。
func StopReasonIs() eval.Scorer {
	return fn{"stop_reason", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		want, ok := eval.ExpectString(c, "stop_reason")
		if !ok {
			return skipped("stop_reason")
		}
		if r.StopReason() == want {
			return passed("stop_reason", "")
		}
		return failed("stop_reason", fmt.Sprintf("stop %q != %q", r.StopReason(), want))
	}}
}

// MaxIterations 断言迭代次数不超过 expect.max_iterations。
func MaxIterations() eval.Scorer {
	return fn{"max_iterations", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		max, ok := eval.ExpectInt(c, "max_iterations")
		if !ok {
			return skipped("max_iterations")
		}
		if r.Iterations() <= max {
			return passed("max_iterations", "")
		}
		return failed("max_iterations", fmt.Sprintf("iters %d > %d", r.Iterations(), max))
	}}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `GO test ./internal/eval/scorers/ -run 'TestContains|TestToolTrajectory|TestStopReason' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/eval/scorers/deterministic.go internal/eval/scorers/deterministic_test.go internal/eval/scorers/doc.go
git commit -m "✨ feat(eval): 确定性打分器（exact/contains/regex/json/轨迹/停止原因/迭代）

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: 性能/成本打分器(`scorers/perf.go`)

**Files:**
- Create: `internal/eval/scorers/perf.go`
- Test: `internal/eval/scorers/perf_test.go`

**Interfaces:**
- Consumes: 同 Task 5 + `eval.RunRecord.Latency`/`.Usage`/`.Iterations()`。
- Produces:
  - `func LatencyBudget() eval.Scorer`(key `latency_ms`,毫秒)
  - `func IterationBudget() eval.Scorer`(key `iteration_budget`)
  - `func TokenBudget() eval.Scorer`(key `token_budget`;`Usage.TotalTokens==0` 且未走非流式时判跳过)
  - `func CostBudget() eval.Scorer`(key `cost_usd`)

- [ ] **Step 1: 写失败测试**

`internal/eval/scorers/perf_test.go`:
```go
package scorers

import (
	"context"
	"testing"
	"time"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/eval"
)

func TestLatencyBudget(t *testing.T) {
	r := eval.RunRecord{Result: &corereact.Result{}, Latency: 500 * time.Millisecond}
	ok := LatencyBudget().Score(context.Background(), eval.Case{Expect: map[string]any{"latency_ms": float64(1000)}}, r)
	if !ok.Passed {
		t.Fatalf("500ms should pass 1000ms budget: %+v", ok)
	}
	bad := LatencyBudget().Score(context.Background(), eval.Case{Expect: map[string]any{"latency_ms": float64(100)}}, r)
	if bad.Passed {
		t.Fatal("500ms should fail 100ms budget")
	}
}

func TestTokenBudget(t *testing.T) {
	r := eval.RunRecord{Result: &corereact.Result{}, Usage: eval.Usage{TotalTokens: 120}}
	if TokenBudget().Score(context.Background(), eval.Case{Expect: map[string]any{"token_budget": float64(100)}}, r).Passed {
		t.Fatal("120 > 100 should fail")
	}
	// 无 usage 数据(流式路径) → 跳过
	r0 := eval.RunRecord{Result: &corereact.Result{}, Usage: eval.Usage{TotalTokens: 0}}
	if !TokenBudget().Score(context.Background(), eval.Case{Expect: map[string]any{"token_budget": float64(100)}}, r0).Skipped {
		t.Fatal("zero usage should skip token budget")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GO test ./internal/eval/scorers/ -run 'TestLatency|TestToken' -v`
Expected: 编译失败 `undefined: LatencyBudget`。

- [ ] **Step 3: 写实现**

`internal/eval/scorers/perf.go`:
```go
package scorers

import (
	"context"
	"fmt"

	"github.com/boxify/api-go/internal/eval"
)

// LatencyBudget 断言运行墙钟延迟不超过 expect.latency_ms 毫秒。
func LatencyBudget() eval.Scorer {
	return fn{"latency_budget", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		budgetMS, ok := eval.ExpectFloat(c, "latency_ms")
		if !ok {
			return skipped("latency_budget")
		}
		gotMS := float64(r.Latency.Milliseconds())
		if gotMS <= budgetMS {
			return passed("latency_budget", fmt.Sprintf("%.0fms <= %.0fms", gotMS, budgetMS))
		}
		return failed("latency_budget", fmt.Sprintf("%.0fms > %.0fms", gotMS, budgetMS))
	}}
}

// IterationBudget 断言迭代次数不超过 expect.iteration_budget。
func IterationBudget() eval.Scorer {
	return fn{"iteration_budget", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		budget, ok := eval.ExpectInt(c, "iteration_budget")
		if !ok {
			return skipped("iteration_budget")
		}
		if r.Iterations() <= budget {
			return passed("iteration_budget", "")
		}
		return failed("iteration_budget", fmt.Sprintf("iters %d > %d", r.Iterations(), budget))
	}}
}

// TokenBudget 断言累计 token 不超过 expect.token_budget。
//
// 累计 token 为 0(如流式路径无 usage)时判跳过,避免误判通过。
func TokenBudget() eval.Scorer {
	return fn{"token_budget", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		budget, ok := eval.ExpectInt(c, "token_budget")
		if !ok {
			return skipped("token_budget")
		}
		if r.Usage.TotalTokens == 0 {
			return skipped("token_budget")
		}
		if int(r.Usage.TotalTokens) <= budget {
			return passed("token_budget", fmt.Sprintf("%d <= %d", r.Usage.TotalTokens, budget))
		}
		return failed("token_budget", fmt.Sprintf("%d > %d", r.Usage.TotalTokens, budget))
	}}
}

// CostBudget 断言折算成本不超过 expect.cost_usd。成本为 0 时判跳过。
func CostBudget() eval.Scorer {
	return fn{"cost_budget", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		budget, ok := eval.ExpectFloat(c, "cost_usd")
		if !ok {
			return skipped("cost_budget")
		}
		if r.Usage.CostUSD == 0 {
			return skipped("cost_budget")
		}
		if r.Usage.CostUSD <= budget {
			return passed("cost_budget", fmt.Sprintf("$%.4f <= $%.4f", r.Usage.CostUSD, budget))
		}
		return failed("cost_budget", fmt.Sprintf("$%.4f > $%.4f", r.Usage.CostUSD, budget))
	}}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `GO test ./internal/eval/scorers/ -run 'TestLatency|TestToken' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/eval/scorers/perf.go internal/eval/scorers/perf_test.go
git commit -m "✨ feat(eval): 性能/成本打分器（延迟/迭代/token/成本预算）

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: LLM-as-judge 打分器(`scorers/judge.go`)

**Files:**
- Create: `internal/eval/scorers/judge.go`
- Test: `internal/eval/scorers/judge_test.go`

**Interfaces:**
- Consumes: `eval.Case`/`eval.RunRecord`/`eval.Scorer`、`llm.Client`、`text/template`、`encoding/json`。
- Produces:
  - `type LLMJudge struct { Client llm.Client; Rubric string; PassThreshold float64 }`
    - `Rubric` 为 `text/template`,可用 `.Query`/`.Answer`/`.Reference`;为空用内置默认 rubric。
    - `PassThreshold` 为 0 时默认 0.5。
  - `func (j LLMJudge) Name() string`(返回 `"llm_judge"`)
  - `func (j LLMJudge) Score(ctx context.Context, c eval.Case, r eval.RunRecord) eval.Score`
    - Client 为 nil → `Score{Skipped:true}`(CI 默认不注入 judge)。
    - 调 `Client.Invoke` 得文本 → 解析首个 JSON 对象 `{"score":0..1,"pass":bool,"reason":"..."}`;解析失败 → `Score{Err}`。
    - `Value=score`;`Passed = pass 字段存在时取之,否则 score>=PassThreshold`。

- [ ] **Step 1: 写失败测试**

`internal/eval/scorers/judge_test.go`:
```go
package scorers

import (
	"context"
	"errors"
	"testing"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/llm"
	"github.com/boxify/api-go/internal/eval"
)

type judgeClient struct{ out string }

func (j judgeClient) Invoke(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (string, error) {
	return j.out, nil
}
func (judgeClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return nil, errors.New("n/a")
}
func (judgeClient) Stream(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan string, error) {
	return nil, errors.New("n/a")
}
func (judgeClient) Embed(ctx context.Context, t []string, d int, o ...llm.EmbeddingOption) ([][]float64, error) {
	return nil, errors.New("n/a")
}
func (judgeClient) EmbedOne(ctx context.Context, t string, d int) ([]float64, error) {
	return nil, errors.New("n/a")
}

func TestLLMJudgePassAndParse(t *testing.T) {
	j := LLMJudge{Client: judgeClient{out: `这是评价 {"score":0.9,"pass":true,"reason":"good"}`}}
	r := eval.RunRecord{Result: &corereact.Result{Answer: "42"}}
	s := j.Score(context.Background(), eval.Case{Query: "q"}, r)
	if !s.Passed || s.Value != 0.9 {
		t.Fatalf("judge = %+v", s)
	}
}

func TestLLMJudgeNilClientSkips(t *testing.T) {
	if !(LLMJudge{}).Score(context.Background(), eval.Case{}, eval.RunRecord{}).Skipped {
		t.Fatal("nil client should skip")
	}
}

func TestLLMJudgeBadJSONErrors(t *testing.T) {
	j := LLMJudge{Client: judgeClient{out: "no json here"}}
	s := j.Score(context.Background(), eval.Case{}, eval.RunRecord{Result: &corereact.Result{}})
	if s.Err == nil {
		t.Fatal("bad json should error")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GO test ./internal/eval/scorers/ -run TestLLMJudge -v`
Expected: 编译失败 `undefined: LLMJudge`。

- [ ] **Step 3: 写实现**

`internal/eval/scorers/judge.go`:
```go
package scorers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/boxify/api-go/internal/core/llm"
	"github.com/boxify/api-go/internal/eval"
)

const defaultRubric = `你是严格的评审。根据问题与回答质量打分,只输出一个 JSON 对象:
{"score": 0到1的小数, "pass": true或false, "reason": "简短理由"}

问题:{{.Query}}
回答:{{.Answer}}
{{if .Reference}}参考答案:{{.Reference}}{{end}}`

// LLMJudge 用模型按 rubric 对回答打分。
//
// Rubric 为 text/template(可用 .Query/.Answer/.Reference),为空用内置默认。
// PassThreshold 为 0 时取 0.5。Client 为 nil 时跳过(CI 默认不注入)。
type LLMJudge struct {
	Client        llm.Client
	Rubric        string
	PassThreshold float64
}

// Name 返回打分器名。
func (j LLMJudge) Name() string { return "llm_judge" }

type judgeVerdict struct {
	Score  float64 `json:"score"`
	Pass   *bool   `json:"pass"`
	Reason string  `json:"reason"`
}

// Score 调用评审模型并解析结构化裁定。
func (j LLMJudge) Score(ctx context.Context, c eval.Case, r eval.RunRecord) eval.Score {
	if j.Client == nil {
		return eval.Score{Scorer: "llm_judge", Skipped: true, Passed: true, Detail: "skipped: no judge client"}
	}
	rubric := j.Rubric
	if rubric == "" {
		rubric = defaultRubric
	}
	tmpl, err := template.New("rubric").Parse(rubric)
	if err != nil {
		return eval.Score{Scorer: "llm_judge", Err: err, ErrText: err.Error()}
	}
	ref, _ := eval.ExpectString(c, "reference")
	var sb strings.Builder
	if err := tmpl.Execute(&sb, map[string]any{"Query": c.Query, "Answer": r.Answer(), "Reference": ref}); err != nil {
		return eval.Score{Scorer: "llm_judge", Err: err, ErrText: err.Error()}
	}
	out, err := j.Client.Invoke(ctx, []*llm.Message{{Role: llm.RoleUser, Content: sb.String()}}, )
	if err != nil {
		return eval.Score{Scorer: "llm_judge", Err: err, ErrText: err.Error()}
	}
	v, err := parseVerdict(out)
	if err != nil {
		return eval.Score{Scorer: "llm_judge", Err: err, ErrText: err.Error()}
	}
	threshold := j.PassThreshold
	if threshold == 0 {
		threshold = 0.5
	}
	passed := v.Score >= threshold
	if v.Pass != nil {
		passed = *v.Pass
	}
	return eval.Score{Scorer: "llm_judge", Value: v.Score, Passed: passed, Detail: v.Reason}
}

// parseVerdict 从模型输出中抽取首个 JSON 对象并解析裁定。
func parseVerdict(out string) (judgeVerdict, error) {
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end < start {
		return judgeVerdict{}, fmt.Errorf("no json object in judge output: %q", out)
	}
	var v judgeVerdict
	if err := json.Unmarshal([]byte(out[start:end+1]), &v); err != nil {
		return judgeVerdict{}, fmt.Errorf("parse judge verdict: %w", err)
	}
	return v, nil
}
```

> **核对点**:确认 `llm.Message` 的字段名(`Role`/`Content`)与角色常量名(`llm.RoleUser` 或 `llm.MessageRoleUser`)。执行前 `Read internal/core/llm/types.go` 校正字面量。去掉 `Invoke(...)` 尾随多余逗号。

- [ ] **Step 4: 跑测试确认通过**

Run: `GO test ./internal/eval/scorers/ -run TestLLMJudge -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/eval/scorers/judge.go internal/eval/scorers/judge_test.go
git commit -m "✨ feat(eval): LLM-as-judge 打分器（rubric 模板 + 结构化裁定解析）

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: 评估器(`Evaluator` / `CaseResult`)

**Files:**
- Create: `internal/eval/evaluator.go`
- Test: `internal/eval/evaluator_test.go`

**Interfaces:**
- Consumes: `Runner`(T4)、`Scorer`/`Score`(T2)、`Dataset`/`Case`(T1)、`RunRecord`(T3)、`Report`(T9 定义;本 Task 产出 `*Report` 值,故 `Report` 结构须在 T9 落地——按顺序执行时 T8 引用 T9 的类型会失败,故 **T8 与 T9 合并为一次编译**:先写 T9 的 `report.go` 类型骨架,再回到 T8。执行者可先建空的 `report.go` 定义 `Report`/`CaseResult` 类型,T9 再补方法)。
- Produces:
  - `type CaseResult struct { CaseID string; Tags []string; Passed bool; Scores []Score; LatencyMS int64; Iterations int; TotalTokens int64; CostUSD float64; StopReason string; Answer string }`(全 JSON tag)。
  - `type Evaluator struct { Runner Runner; Scorers []Scorer }`
  - `func (e *Evaluator) Run(ctx context.Context, ds *Dataset) (*Report, error)`

> **顺序说明**:`CaseResult` 定义放 `report.go`(T9),因为它主要服务序列化/报告。T8 的 `evaluator.go` 消费它。为让 TDD 可跑,**先在 T9 建 `report.go` 的类型定义(`Report`、`CaseResult`)**,再实现 T8,再补 T9 的方法。下面 T8 测试假定 `report.go` 类型已存在。

- [ ] **Step 1: 先建 `report.go` 类型骨架(供 T8 编译)**

`internal/eval/report.go`(仅类型,方法在 T9 补):
```go
package eval

// CaseResult 表示一条用例的评估结果(可序列化投影,不含 *react.Result)。
type CaseResult struct {
	CaseID      string   `json:"case_id"`
	Tags        []string `json:"tags,omitempty"`
	Passed      bool     `json:"passed"`
	Scores      []Score  `json:"scores"`
	LatencyMS   int64    `json:"latency_ms"`
	Iterations  int      `json:"iterations"`
	TotalTokens int64    `json:"total_tokens"`
	CostUSD     float64  `json:"cost_usd"`
	StopReason  string   `json:"stop_reason"`
	Answer      string   `json:"answer,omitempty"`
}

// ScorerAgg 是单个打分器在数据集上的聚合。
type ScorerAgg struct {
	Scorer    string  `json:"scorer"`
	Runs      int     `json:"runs"`
	Passed    int     `json:"passed"`
	Failed    int     `json:"failed"`
	Skipped   int     `json:"skipped"`
	Errored   int     `json:"errored"`
	MeanValue float64 `json:"mean_value"`
}

// Report 是一次评估的完整结果。
type Report struct {
	Dataset  string               `json:"dataset"`
	Cases    []CaseResult         `json:"cases"`
	PassRate float64              `json:"pass_rate"`
	Scorers  map[string]ScorerAgg `json:"scorers"`
}
```

- [ ] **Step 2: 写失败测试**

`internal/eval/evaluator_test.go`:
```go
package eval

import (
	"context"
	"testing"

	coreagent "github.com/boxify/api-go/internal/core/agent"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
)

type stubRunner struct{ answer string }

func (s stubRunner) Run(ctx context.Context, c Case) (RunRecord, error) {
	return RunRecord{Result: &corereact.Result{Answer: s.answer, Iterations: 1, StoppedBy: coreagent.StopFinalAnswer}}, nil
}

type containsScorer struct{}

func (containsScorer) Name() string { return "contains" }
func (containsScorer) Score(_ context.Context, c Case, r RunRecord) Score {
	want, ok := ExpectString(c, "want")
	if !ok {
		return Score{Scorer: "contains", Skipped: true, Passed: true}
	}
	if r.Answer() == want {
		return Score{Scorer: "contains", Passed: true, Value: 1}
	}
	return Score{Scorer: "contains", Passed: false}
}

func TestEvaluatorAggregates(t *testing.T) {
	ds := &Dataset{Name: "t", Cases: []Case{
		{ID: "a", Expect: map[string]any{"want": "ok"}},
		{ID: "b", Expect: map[string]any{"want": "nope"}},
	}}
	e := &Evaluator{Runner: stubRunner{answer: "ok"}, Scorers: []Scorer{containsScorer{}}}
	rep, err := e.Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Cases) != 2 {
		t.Fatalf("cases = %d", len(rep.Cases))
	}
	if rep.PassRate != 0.5 {
		t.Fatalf("pass rate = %v, want 0.5", rep.PassRate)
	}
	agg := rep.Scorers["contains"]
	if agg.Passed != 1 || agg.Failed != 1 {
		t.Fatalf("agg = %+v", agg)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `GO test ./internal/eval/ -run TestEvaluatorAggregates -v`
Expected: 编译失败 `undefined: Evaluator`。

- [ ] **Step 4: 写实现**

`internal/eval/evaluator.go`:
```go
package eval

import "context"

// Evaluator 用一组打分器在数据集上评估一个 Runner。
type Evaluator struct {
	Runner  Runner
	Scorers []Scorer
}

// Run 串行跑完数据集,逐用例调 Runner 与全部 Scorer,聚合成 Report。
func (e *Evaluator) Run(ctx context.Context, ds *Dataset) (*Report, error) {
	rep := &Report{Dataset: ds.Name, Scorers: map[string]ScorerAgg{}}
	valueSum := map[string]float64{}
	valueCount := map[string]int{}

	for _, c := range ds.Cases {
		record, err := e.Runner.Run(ctx, c)
		if err != nil {
			return nil, err
		}
		cr := CaseResult{
			CaseID:      c.ID,
			Tags:        c.Tags,
			LatencyMS:   record.Latency.Milliseconds(),
			Iterations:  record.Iterations(),
			TotalTokens: record.Usage.TotalTokens,
			CostUSD:     record.Usage.CostUSD,
			StopReason:  record.StopReason(),
			Answer:      record.Answer(),
			Passed:      true,
		}
		for _, s := range e.Scorers {
			score := s.Score(ctx, c, record)
			if score.Err != nil && score.ErrText == "" {
				score.ErrText = score.Err.Error()
			}
			cr.Scores = append(cr.Scores, score)

			agg := rep.Scorers[score.Scorer]
			agg.Scorer = score.Scorer
			agg.Runs++
			switch {
			case score.Err != nil:
				agg.Errored++
				cr.Passed = false
			case score.Skipped:
				agg.Skipped++
			case score.Passed:
				agg.Passed++
				valueSum[score.Scorer] += score.Value
				valueCount[score.Scorer]++
			default:
				agg.Failed++
				cr.Passed = false
				valueSum[score.Scorer] += score.Value
				valueCount[score.Scorer]++
			}
			rep.Scorers[score.Scorer] = agg
		}
		rep.Cases = append(rep.Cases, cr)
	}

	for name, agg := range rep.Scorers {
		if valueCount[name] > 0 {
			agg.MeanValue = valueSum[name] / float64(valueCount[name])
			rep.Scorers[name] = agg
		}
	}
	if len(rep.Cases) > 0 {
		passed := 0
		for _, cr := range rep.Cases {
			if cr.Passed {
				passed++
			}
		}
		rep.PassRate = float64(passed) / float64(len(rep.Cases))
	}
	return rep, nil
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `GO test ./internal/eval/ -run TestEvaluatorAggregates -v`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/eval/evaluator.go internal/eval/evaluator_test.go internal/eval/report.go
git commit -m "✨ feat(eval): Evaluator 逐用例打分与聚合 + Report/CaseResult 类型骨架

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: 报告输出、回归对比与门禁(`report.go` 方法)

**Files:**
- Modify: `internal/eval/report.go`(补方法)
- Test: `internal/eval/report_test.go`

**Interfaces:**
- Consumes: `Report`/`CaseResult`/`Score`(T8 骨架)。
- Produces:
  - `func (r *Report) WriteJSON(w io.Writer) error` —— 缩进 JSON。
  - `func (r *Report) WriteTable(w io.Writer) error` —— 人读表格(用例 × 每 scorer:PASS/FAIL/SKIP)。
  - `func LoadReport(path string) (*Report, error)` —— 从 JSON 载入基线。
  - `type Regression struct { CaseID, Scorer, Detail string }`
  - `type RegressionReport struct { NewFailures []Regression }`
  - `func (r *Report) Diff(baseline *Report) *RegressionReport` —— 对每个 (case_id, scorer):基线 Passed 且当前非 Skipped 非 Passed → 记为新失败。
  - `func AssertNoRegression(t testing.TB, current, baseline *Report)` —— 有新失败即 `t.Errorf` 每条。

- [ ] **Step 1: 写失败测试**

`internal/eval/report_test.go`:
```go
package eval

import (
	"bytes"
	"encoding/json"
	"testing"
)

func mkReport(caseID, scorer string, passed bool) *Report {
	return &Report{
		Dataset: "d",
		Cases: []CaseResult{{
			CaseID: caseID,
			Passed: passed,
			Scores: []Score{{Scorer: scorer, Passed: passed}},
		}},
	}
}

func TestDiffDetectsNewFailure(t *testing.T) {
	base := mkReport("a", "contains", true)
	cur := mkReport("a", "contains", false)
	reg := cur.Diff(base)
	if len(reg.NewFailures) != 1 || reg.NewFailures[0].CaseID != "a" {
		t.Fatalf("regressions = %+v", reg.NewFailures)
	}
	// 反向:基线本就失败 → 不算回归
	if len(mkReport("a", "contains", true).Diff(mkReport("a", "contains", false)).NewFailures) != 0 {
		t.Fatal("baseline-failed should not count as new failure")
	}
}

func TestWriteJSONRoundTrip(t *testing.T) {
	rep := mkReport("a", "contains", true)
	rep.PassRate = 1
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var back Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Cases[0].CaseID != "a" || back.PassRate != 1 {
		t.Fatalf("roundtrip = %+v", back)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GO test ./internal/eval/ -run 'TestDiff|TestWriteJSON' -v`
Expected: 编译失败 `Report has no field or method Diff`。

- [ ] **Step 3: 写实现(在 `report.go` 追加)**

```go
import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"text/tabwriter"
)

// WriteJSON 以缩进 JSON 写出报告。
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteTable 以人读表格写出报告:每行一条用例,列出通过/总数与各打分器状态。
func (r *Report) WriteTable(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "CASE\tPASSED\tSCORES\n")
	for _, c := range r.Cases {
		status := ""
		for i, s := range c.Scores {
			if i > 0 {
				status += " "
			}
			switch {
			case s.Err != nil || s.ErrText != "":
				status += s.Scorer + "=ERR"
			case s.Skipped:
				status += s.Scorer + "=SKIP"
			case s.Passed:
				status += s.Scorer + "=PASS"
			default:
				status += s.Scorer + "=FAIL"
			}
		}
		fmt.Fprintf(tw, "%s\t%v\t%s\n", c.CaseID, c.Passed, status)
	}
	fmt.Fprintf(tw, "---\tpass_rate\t%.2f%%\n", r.PassRate*100)
	return tw.Flush()
}

// LoadReport 从 JSON 文件载入基线报告。
func LoadReport(path string) (*Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("parse report: %w", err)
	}
	return &rep, nil
}

// Regression 表示一条相对基线新出现的打分失败。
type Regression struct {
	CaseID string `json:"case_id"`
	Scorer string `json:"scorer"`
	Detail string `json:"detail"`
}

// RegressionReport 汇总相对基线的新失败。
type RegressionReport struct {
	NewFailures []Regression `json:"new_failures"`
}

// Diff 对比基线,找出基线通过而当前失败(非跳过)的打分。
func (r *Report) Diff(baseline *Report) *RegressionReport {
	baseScore := map[string]bool{} // key: caseID|scorer -> passed
	for _, c := range baseline.Cases {
		for _, s := range c.Scores {
			baseScore[c.CaseID+"|"+s.Scorer] = s.Passed && !s.Skipped
		}
	}
	out := &RegressionReport{}
	for _, c := range r.Cases {
		for _, s := range c.Scores {
			if s.Skipped {
				continue
			}
			wasPass, known := baseScore[c.CaseID+"|"+s.Scorer]
			if known && wasPass && !s.Passed {
				out.NewFailures = append(out.NewFailures, Regression{CaseID: c.CaseID, Scorer: s.Scorer, Detail: s.Detail})
			}
		}
	}
	return out
}

// AssertNoRegression 在有相对基线的新失败时使测试失败。
func AssertNoRegression(t testing.TB, current, baseline *Report) {
	t.Helper()
	reg := current.Diff(baseline)
	for _, f := range reg.NewFailures {
		t.Errorf("regression: case %q scorer %q now fails (%s)", f.CaseID, f.Scorer, f.Detail)
	}
}
```

> 注:`report.go` 引入 `testing` 供 `AssertNoRegression`。这会让 `testing` 进入非测试文件的 import——可接受(项目若禁止,则把 `AssertNoRegression` 移到 `assert.go` 且仍非 `_test.go`,因为它要被 tag `eval` 的外部测试调用)。执行者确认 `go vet` 无警告即可。

- [ ] **Step 4: 跑测试确认通过**

Run: `GO test ./internal/eval/ -run 'TestDiff|TestWriteJSON' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/eval/report.go internal/eval/report_test.go
git commit -m "✨ feat(eval): 报告输出（JSON/表格）+ 基线回归 Diff + AssertNoRegression 门禁

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: 端到端回归门禁 + 数据集/基线夹具 + 文档

**Files:**
- Create: `internal/eval/eval_gate_test.go`(build tag `eval`)
- Create: `internal/eval/testdata/datasets/gate.json`
- Create: `internal/eval/testdata/baselines/gate.json`
- Create: `internal/eval/doc.go`
- Create: `docs/EVAL.md`

**Interfaces:**
- Consumes: 全部前序导出物 + `scorers` 包。
- Produces: 可用 `go test -tags eval` 触发的 hermetic 回归门禁(用工具调用 fake client,无网络)。

- [ ] **Step 1: 写数据集与基线夹具**

`internal/eval/testdata/datasets/gate.json`:
```json
{
  "name": "gate",
  "cases": [
    {
      "id": "final-answer",
      "query": "回答:42",
      "expect": {
        "contains": ["42"],
        "stop_reason": "final_answer",
        "max_iterations": 2,
        "latency_ms": 60000,
        "token_budget": 1000
      }
    }
  ]
}
```

`internal/eval/testdata/baselines/gate.json`(首个绿色报告的存档;先写期望态,门禁据此判回归):
```json
{
  "dataset": "gate",
  "pass_rate": 1,
  "cases": [
    {
      "case_id": "final-answer",
      "passed": true,
      "scores": [
        {"scorer": "contains", "passed": true, "value": 1},
        {"scorer": "stop_reason", "passed": true, "value": 1},
        {"scorer": "max_iterations", "passed": true, "value": 1},
        {"scorer": "latency_budget", "passed": true, "value": 1},
        {"scorer": "token_budget", "passed": true, "value": 1}
      ]
    }
  ],
  "scorers": {}
}
```

- [ ] **Step 2: 写门禁测试(tag eval)**

`internal/eval/eval_gate_test.go`:
```go
//go:build eval

package eval_test

import (
	"context"
	"testing"

	"github.com/boxify/api-go/internal/core/llm"
	"github.com/boxify/api-go/internal/eval"
	"github.com/boxify/api-go/internal/eval/scorers"
)

// gateClient 是实现 ToolCallingClient 的 hermetic fake:无工具调用即终局答案,携带 usage。
type gateClient struct{}

func (gateClient) Invoke(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (string, error) {
	return "Thought: done\nFinal Answer: 42", nil
}
func (gateClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return &llm.LLMResult{Text: "42", Usage: llm.TokenUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}, Model: "gate"}, nil
}
func (gateClient) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return &llm.LLMResult{Text: "42", Usage: llm.TokenUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}, Model: "gate"}, nil
}
func (gateClient) Stream(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan string, error) {
	return nil, nil
}
func (gateClient) Embed(ctx context.Context, t []string, d int, o ...llm.EmbeddingOption) ([][]float64, error) {
	return nil, nil
}
func (gateClient) EmbedOne(ctx context.Context, t string, d int) ([]float64, error) { return nil, nil }

func TestEvalGate(t *testing.T) {
	ds, err := eval.LoadDataset("testdata/datasets/gate.json")
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	runner := &eval.HarnessRunner{Client: gateClient{}}
	e := &eval.Evaluator{
		Runner: runner,
		Scorers: []eval.Scorer{
			scorers.Contains(),
			scorers.StopReasonIs(),
			scorers.MaxIterations(),
			scorers.LatencyBudget(),
			scorers.TokenBudget(),
		},
	}
	rep, err := e.Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("eval run: %v", err)
	}
	_ = rep.WriteTable(testWriter{t})
	baseline, err := eval.LoadReport("testdata/baselines/gate.json")
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	eval.AssertNoRegression(t, rep, baseline)
	if rep.PassRate != 1 {
		t.Fatalf("pass rate = %v, want 1", rep.PassRate)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }
```

> **核对点**:①`gateClient` 实现 `ToolCallingClient` → usage 装饰器暴露之 → harness/planner 走非流式工具调用 → usage 被捕获、`token_budget` 生效(非跳过)。若 planner 对"无 tool call 的 LLMResult"不判终局,改为让 `InvokeWithTools` 返回带 `Text` 且无 `ToolCalls`,并 `Read internal/core/agent/react/planner.go` 确认终局判定;必要时退回纯文本 `gateClient`(去掉 InvokeWithTools),此时删掉 `token_budget` 断言(会跳过)并同步基线。

- [ ] **Step 3: 跑门禁确认通过**

Run: `GO test ./internal/eval/ -tags eval -run TestEvalGate -v`
Expected: PASS,日志打印报告表格。

- [ ] **Step 4: 确认常规测试不受 tag 影响**

Run: `GO test ./internal/eval/... -v` 与 `GO vet ./internal/eval/...`
Expected: 全绿;`eval_gate_test.go` 不参与(无 tag 时不编译)。

- [ ] **Step 5: 写包文档与使用指南**

`internal/eval/doc.go`:
```go
// Package eval 提供业务无关的 Agent 评估体系。
//
// 四层正交:Dataset(数据集)→ Runner(SUT 运行器,默认 HarnessRunner 跑 harness 包裹的
// agent)→ Scorer(打分器:确定性/性能成本/LLM-judge,见子包 scorers)→ Report(报告与
// 基线回归门禁)。运行器内部注入仅暴露非流式能力的 usage 捕获装饰器,并可按 Case.Cassette
// 走 harness 回放做到 hermetic。
//
// 一期为离线回归评估;线上评估(二期)复用同一 Scorer 接口,经 harness.Metrics/OTel
// seam 发分数,详见 docs/EVAL.md。
package eval
```

`docs/EVAL.md`:
```markdown
# Agent 评估体系（internal/eval）

业务无关的四层评估框架:**数据集 → 运行器 → 打分器 → 报告**。SUT 为 harness 包裹的 agent。

## 快速开始

```go
runner := &eval.HarnessRunner{Client: myClient, Registry: myTools, Cost: myCostModel}
e := &eval.Evaluator{Runner: runner, Scorers: []eval.Scorer{
    scorers.Contains(), scorers.ToolTrajectory(), scorers.StopReasonIs(),
    scorers.LatencyBudget(), scorers.TokenBudget(),
    // scorers.LLMJudge{Client: judgeClient, PassThreshold: 0.6}, // 可选
}}
ds, _ := eval.LoadDataset("path/to/dataset.json")
rep, _ := e.Run(ctx, ds)
rep.WriteTable(os.Stdout)
```

## 数据集格式

见 `internal/eval/testdata/datasets/*.json`。每条用例:`id`、`query`(或 `messages`)、
`tags`、`expect`(自由期望,各打分器各取所需)、`cassette`(可选,hermetic 回放)。

`expect` 支持的键:`answer`(exact)、`contains`、`regex`、`json_valid`、`tools`、
`stop_reason`、`max_iterations`、`latency_ms`、`iteration_budget`、`token_budget`、
`cost_usd`、`reference`(judge 用)。缺键的打分器自动跳过。

## CI 回归门禁

用 build tag `eval` 的测试载入数据集 + 基线,跑 `eval.AssertNoRegression`:

```bash
go test ./internal/eval/ -tags eval -run TestEvalGate -v
```

不带 `-tags eval` 时门禁测试不编译,不拖累常规 `go test ./...`。

## usage/成本捕获限制

token/成本仅在**非流式**模型路径(`InvokeResult`/`InvokeWithTools`)可得——`StreamEvent`
不带 usage。运行器的 usage 装饰器故意只暴露非流式能力,使 eval 走非流式从而可计量。
纯文本 ReAct(走 `Invoke`)无 usage,`token_budget`/`cost_budget` 对其自动跳过。

## 用真实模型录制 cassette(可复现)

```go
// 录制:用真实 client 跑一次,落盘磁带
h := harness.New(realClient, reg, harness.WithDeterminism(harness.DeterminismRecord, "case.json"))
// 之后把 case.json 放进 testdata/cassettes/,数据集用例填 "cassette": "../cassettes/case.json"
// 回放:HarnessRunner 见 Case.Cassette 非空即自动走 DeterminismReplay
```

## 二期:线上生产评估(暂缓)

复用同一 `Scorer` 接口对真实运行(经 harness user-hook 捕获 Result)打分,分数经
`harness.Metrics`/OTel seam 发出(`agent_eval_score{scorer}` + span 属性),任意 OTLP
后端(含本地 Langfuse)可视化。不写 Langfuse 专用代码。
```

- [ ] **Step 6: 全量校验**

Run:
```
GO build ./...
GO vet ./internal/eval/...
GO test ./internal/eval/... -v
GO test ./internal/eval/ -tags eval -v
```
Expected: 全绿。

- [ ] **Step 7: 提交**

```bash
git add internal/eval/eval_gate_test.go internal/eval/testdata/ internal/eval/doc.go docs/EVAL.md
git commit -m "✨ feat(eval): 端到端回归门禁（tag eval）+ 数据集/基线夹具 + 包文档与 EVAL.md

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- 数据集/`Case.Expect` 自由 map → T1、T2 ✅
- SUT=harness 包裹 agent → T4 `HarnessRunner` ✅
- 确定性打分器族 → T5 ✅
- 性能/成本族 → T6 ✅
- LLM-judge → T7 ✅
- Go 原生报告(JSON/表格/回归门禁)→ T9 ✅
- 确定性/CI(tag eval + cassette 回放)→ T4(回放注入)、T10(门禁)✅
- usage 捕获限制(非流式)→ T3 装饰器 + 全程文档 ✅
- 二期线上评估留档 → doc.go + EVAL.md ✅

**Placeholder scan:** 无 TBD/TODO;每步含真实代码。少量"核对点"是让执行者对齐既有真实签名(`CostTable` 结构、`llm.Message` 字段名、planner 终局判定),非占位符——附了具体 `Read` 指令与回退方案。

**Type consistency:** `Case/Dataset/Score/Scorer/RunRecord/Usage/Runner/HarnessRunner/Evaluator/CaseResult/ScorerAgg/Report/Regression/RegressionReport` 跨任务命名一致;`wrapUsage`、`ExpectString/Strings/Float/Int`、`ToolTrajectory()` 签名前后一致。scorers 子包不依赖 eval 非导出助手(自建 `passed/failed/skipped`)。

**已知执行期风险(附回退):** T10 的 `token_budget` 是否生效取决于 planner 对"工具调用 client 返回无 tool-call 结果"的终局判定;若不成立,回退纯文本 fake 并移除 token 断言(文档已述该限制),门禁其余四族仍验证完整链路。
