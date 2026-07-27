# Agent 评估体系设计（`internal/eval`）

日期：2026-07-26
状态：已定稿，待实现

## 目标

为服务端 Agent 建立**业务无关、可复现、CI 友好**的评估体系:用数据集在 harness 包裹的 agent 上跑用例,用多族打分器给结果打分,产出 Go 原生报告并对比基线做回归门禁。与已有的 `harness`(四支柱 + cassette 录放)和 `internal/observability/otel` 同构、复用其地基。

## 范围决策(已确认)

- **场景**:两者都要,分两期。一期=离线回归评估(本设计主体);二期=线上生产评估(设计好、暂缓)。
- **被评估对象(SUT)**:harness 包裹的 agent(最贴近生产)。
- **打分器**:确定性断言 + LLM-as-judge + 性能/成本,三族全要。
- **结果去向**:Go 原生报告(JSON + 人读表格 + 回归门禁);不接 Langfuse push。

自行拍板的细节默认:
- `Case.Expect` 用自由 `map[string]any`,各 scorer 各取所需(不为每类 scorer 建强类型字段)。
- 一期实现 `LLMJudge`,但 CI 默认门禁只跑确定性+性能族;judge 需显式注入 client(live 或 replay 磁带)才参与。
- 目录、中文 doc 注释、TDD 均沿用 `harness`/`otel` 既有约定。模块 `github.com/boxify/api-go`。

## 非目标

- 不引入外部评估框架(promptfoo/ragas 等)。
- 不写 Langfuse 专用代码(二期经通用 OTel seam 发分数,任意 OTLP 后端可视化)。
- 不做并行用例执行的强并发调度(一期串行 + 可选简单并发,YAGNI)。
- 不覆盖流式路径的 token 用量归集(与现有 harness 局限一致,文档标注)。

## 架构:四层正交

```
Dataset (数据集)  →  Runner (SUT 运行器)  →  Scorer[] (打分器)  →  Report (报告/门禁)
```

四层通过窄接口解耦,可独立理解与测试。唯一"聪明"复用点:Runner 直接用 harness 的 cassette **回放**,让确定性/性能/轨迹三族在 CI 里 hermetic;LLM-judge 用判官磁带回放做到可复现。

## 核心类型(seams)

```go
// case.go
type Case struct {
    ID       string
    Query    string             // → react.Input.Query
    Messages []*llm.Message     // → react.Input.Messages(可选)
    Tags     []string           // 分组/过滤
    Expect   map[string]any     // 自由期望,scorer 各取所需
    Cassette string             // 可选:hermetic 回放磁带路径(相对数据集文件)
}
type Dataset struct {
    Name  string
    Cases []Case
}
func LoadDataset(path string) (*Dataset, error)   // 从 JSON 载入

// record.go
type Usage struct {                 // 跨调用累加的 token/成本
    InputTokens, OutputTokens, TotalTokens int64
    CostUSD float64
}
type RunRecord struct {
    *react.Result                   // Answer / Steps(轨迹) / Iterations / StoppedBy
    Latency time.Duration
    Usage   Usage
    Err     error
}
func (r RunRecord) ToolTrajectory() []string   // 从 Steps 抽出按序调用的工具名

// score.go
type Score struct {
    Scorer string
    Value  float64        // 0..1
    Passed bool
    Detail string
    Err    error
}
type Scorer interface {                          // ★ 一期/二期共用
    Name() string
    Score(ctx context.Context, c Case, r RunRecord) Score
}

// runner.go
type Runner interface {
    Run(ctx context.Context, c Case) (RunRecord, error)   // SUT 适配器
}
```

## 一、数据集(`case.go`)

- JSON 文件放 `internal/eval/testdata/datasets/*.json`,一个文件一个 `Dataset`。
- `LoadDataset` 读文件 + 反序列化 + 基本校验(ID 非空且唯一)。
- `Case.Cassette` 若非空,按相对数据集文件目录解析为绝对路径,交给 Runner 走回放。

数据集样例:
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
        "latency_ms": 8000
      },
      "cassette": "cassettes/greet-basic.json"
    }
  ]
}
```

## 二、运行器(`runner.go`)——SUT = harness 包裹的 agent

`HarnessRunner` 由调用方注入一个"harness 工厂"构造,保持 eval 包对具体 client/registry/opts 无耦合:

```go
type HarnessFactory func(c Case) (*harness.Harness, error)
func NewHarnessRunner(factory HarnessFactory) *HarnessRunner
```

`Run(ctx, c)` 流程:
1. `input := react.Input{Query: c.Query, Messages: c.Messages}`。
2. 计时开始 → `h.Run(ctx, input)` → 计时结束得 `Latency`。
3. 组装 `RunRecord{Result, Latency, Usage, Err}`。

**Usage 捕获**:harness `Metrics` 接口只有 counter/histogram,不含 token。故 token/成本用**client 层求和装饰器**捕获(与 harness `budgetClient` 同理):runner 提供的工厂应把底层 client 先包一层 `usageClient`(累加每次 `LLMResult.Usage` + 经 `CostModel` 折算 `CostUSD`),再交给 `harness.New`。runner 读该装饰器的累加值填 `RunRecord.Usage`。eval 包提供 `WrapUsage(client, cost) (llm.Client, *Usage)` 辅助函数供工厂使用。

**执行模式**:live(真实模型)/ replay(`Case.Cassette` 非空 → 工厂用 `harness.WithDeterminism(DeterminismReplay, path)`)。回放路径下确定性/性能/轨迹三族全部无网络可复现。

## 三、打分器(`scorers/`)——三族

每个 scorer 是独立、无状态、可组合的 `Scorer` 实现,从 `Case.Expect` 取阈值/期望,读 `RunRecord` 判定。

**确定性(`scorers/deterministic.go`)**
- `ExactMatch` — `Answer` 与 `expect.answer` 全等(可选规整空白/大小写)。
- `Contains` — `Answer` 含 `expect.contains`(字符串或数组,全含才过)。
- `Regex` — `Answer` 匹配 `expect.regex`。
- `JSONValid` — `Answer` 是合法 JSON。
- `JSONSchema` — `Answer` 符合 `expect.schema`(用现成 schema 校验库,若无则退化为字段存在性检查)。
- `ToolTrajectory` — `RunRecord.ToolTrajectory()` 满足 `expect.tools`(支持子集 `contains`、精确有序 `exact` 两模式)。
- `StopReasonIs` — `StoppedBy == expect.stop_reason`。
- `MaxIterations` — `Iterations <= expect.max_iterations`。

**性能/成本(`scorers/perf.go`)**
- `LatencyBudget` — `Latency <= expect.latency_ms`。
- `IterationBudget` — `Iterations <= expect.iteration_budget`。
- `TokenBudget` — `Usage.TotalTokens <= expect.token_budget`。
- `CostBudget` — `Usage.CostUSD <= expect.cost_usd`。

**LLM-as-judge(`scorers/judge.go`)**
- `LLMJudge{Client llm.Client, Rubric string}` — 用 rubric 模板拼 prompt(注入 query / answer / 可选 reference),调用 `Client` 得结构化 `{"score":0..1,"pass":bool,"reason":"..."}`,解析填 `Score`。
- 解析失败或 client 为 nil → `Score{Err}`(不静默判过)。
- CI 可复现:注入的 `Client` 用回放磁带即可确定;CI 默认门禁不含 judge,除非显式注入。

打分约定:`Expect` 中缺对应键的 scorer 返回"跳过"(`Passed=true, Detail="skipped: no expectation"`),避免为每条用例配全部键。

## 四、评估器 + 报告(`evaluator.go` / `report.go`)

```go
type Evaluator struct {
    Runner  Runner
    Scorers []Scorer
    // 可选:Concurrency int(默认 1 串行)
}
type CaseResult struct {
    Case   Case
    Record RunRecord
    Scores []Score
    Passed bool          // 全部非跳过 Score 皆 Passed
}
type Report struct {
    Dataset string
    Cases   []CaseResult
    // 聚合:总 pass rate、分 scorer pass rate 与均值
}
func (e *Evaluator) Run(ctx, ds *Dataset) (*Report, error)
```

报告出口(全 Go 原生):
- `report.WriteJSON(w)` — 结构化落盘,供 baseline 存档与 diff。
- `report.WriteTable(w)` — 人读表格(用例 × scorer,PASS/FAIL/SKIP + 值)。
- `report.Diff(baseline *Report) *RegressionReport` — 逐用例/逐 scorer 对比,标出**新失败**(基线过、现在挂)。
- `AssertNoRegression(t testing.TB, baseline *Report)` — 有新失败即 `t.Errorf`,供 `go test` 当门禁。

## 五、确定性 & CI

- `eval_test.go`(build tag `eval`,与现有 `manual` 风格一致)载入数据集 + 可选 baseline JSON,构造回放 runner + 确定性/性能 scorer,跑 `Evaluator.Run`,写报告 + `AssertNoRegression`。
- 不带 `-tags eval` 时该测试不编译,不拖累常规 `go test ./...`。
- 磁带用 harness `DeterminismRecord` 预先录一次(live)后提交到 `testdata`。
- 文档写明:`go test ./internal/eval/... -tags eval -run TestEval` 的跑法与录带流程。

## 六、目录

```
internal/eval/
  doc.go
  case.go          // Case, Dataset, LoadDataset
  record.go        // RunRecord, Usage, ToolTrajectory, WrapUsage
  score.go         // Score, Scorer
  runner.go        // Runner, HarnessRunner, HarnessFactory
  evaluator.go     // Evaluator, CaseResult
  report.go        // Report, Diff, WriteJSON/WriteTable, AssertNoRegression
  scorers/
    deterministic.go
    perf.go
    judge.go
  testdata/
    datasets/*.json
    cassettes/*.json
  *_test.go
eval_test.go        // build tag: eval —— CI 回归门禁
```

## 二期:线上生产评估(暂缓,设计留档)

- 复用**同一 `Scorer` 接口**。经 harness user-hook 在真实运行结束捕获 `react.Result` → 组装 `RunRecord`(线上无 reference,仅跑无需 reference 的 scorer:轨迹/性能/停止原因/无参考 judge)。
- 每个 `Score` 经**已有的 `harness.Metrics`/OTel seam** 发出:`agent_eval_score{scorer}` gauge/histogram + 写入当次 run span 的属性。
- 采样率可配,失败不阻断主流程。
- 任意 OTLP 后端(含已起的本地 Langfuse)即可可视化,无 Langfuse 专用代码。

## 验证标准

- `go build ./...` 通过、`go vet` 干净。
- 各 scorer、runner、evaluator、report/diff 单测覆盖(TDD)。
- 一条端到端:录带 → 回放 runner → 三族 scorer → 报告 → `AssertNoRegression` 全绿。
- `make docs` 无漂移(如适用)。
```
