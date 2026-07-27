# Agent 评估体系（internal/eval）

业务无关的四层评估框架:**数据集 → 运行器 → 打分器 → 报告**。SUT(被评估对象)为 harness 包裹的 agent。

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
`cost_usd`、`reference`(judge 用)。缺键的打分器自动跳过(SKIP,不计通过/失败)。

`tools` 两种写法:字符串数组(子集 contains 模式)或 `{"mode":"exact|contains","names":[...]}`。

## 打分器(scorers 子包)

- **确定性**:`ExactMatch` `Contains` `Regex` `JSONValid` `ToolTrajectory` `StopReasonIs` `MaxIterations`。
- **性能/成本**:`LatencyBudget` `IterationBudget` `TokenBudget` `CostBudget`。
- **LLM-as-judge**:`LLMJudge{Client, Rubric, PassThreshold}` —— rubric 为 `text/template`(可用 `.Query/.Answer/.Reference`),模型输出 `{"score","pass","reason"}`;Client 为 nil 时跳过。

## CI 回归门禁

用 build tag `eval` 的测试载入数据集 + 基线,用 `Report.Diff(baseline).Err()` 判回归
(报告层不依赖 `testing`,由测试侧决定 `t.Fatal`/退出码):

```go
if err := current.Diff(baseline).Err(); err != nil { t.Fatal(err) }
```


```bash
go test ./internal/eval/ -tags eval -run TestEvalGate -v
```

参考实现:`internal/eval/eval_gate_test.go`,基线存于 `testdata/baselines/*.json`。
`Report.Diff(baseline)` 只把**基线通过、当前失败(非跳过)**的打分记为新失败——新增用例或
基线本就失败的不算回归。不带 `-tags eval` 时门禁测试不编译,不拖累常规 `go test ./...`。

更新基线:跑一次绿色评估 → `rep.WriteJSON` 落盘 → 覆盖 `testdata/baselines/<ds>.json`。

## usage/成本捕获限制

token/成本仅在**非流式**模型路径(`InvokeResult`/`InvokeWithTools`)可得——`StreamEvent`
不带 usage。运行器的 usage 装饰器(`wrapUsage`)故意只暴露非流式能力,使 eval 走非流式
从而可计量。纯文本 ReAct(走 `Invoke`)无 usage,`token_budget`/`cost_budget` 对其自动跳过。

## 用真实模型录制 cassette(可复现回放)

```go
// 录制:用真实 client 跑一次,落盘磁带
h := harness.New(realClient, reg, harness.WithDeterminism(harness.DeterminismRecord, "case.json"))
_, _ = h.Run(ctx, react.Input{Query: "..."})
// 把 case.json 放进 testdata/cassettes/,数据集用例填 "cassette": "../cassettes/case.json"
// 回放:HarnessRunner 见 Case.Cassette 非空即自动走 DeterminismReplay,无需网络
```

## 二期:线上生产评估(暂缓,设计留档)

复用同一 `eval.Scorer` 接口对真实运行(经 harness user-hook 捕获 `react.Result`)打分,
分数经 `harness.Metrics`/OTel seam 发出(`agent_eval_score{scorer}` + span 属性),任意
OTLP 后端(含本地 Langfuse)可视化。不写 Langfuse 专用代码。
```
