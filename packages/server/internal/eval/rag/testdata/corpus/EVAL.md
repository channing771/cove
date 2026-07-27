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

## RAG 检索评测(internal/eval/rag)

RAG 模块的核心产出是**检索**,故单独评测检索质量(SUT 是检索器,不是整个 agent)。复用
本包的数据集/报告/回归门禁,新增检索专用 Runner 与 IR 指标打分器。

```go
runner := &rag.RetrievalRunner{Retriever: myRetriever, TopK: 5}
e := &rag.Evaluator{Runner: runner, Scorers: []rag.Scorer{
    rag.RecallAtK(), rag.PrecisionAtK(), rag.HitRate(), rag.MRR(), rag.NDCG(),
    // rag.ContextRelevance{Client: judgeClient}, // 可选 LLM-judge,无需 golden
}}
ds, _ := eval.LoadDataset("path/to/retrieval.json")
rep, _ := e.Run(ctx, ds)           // 产出的是同一个 eval.Report,可 Diff/WriteJSON/门禁
```

**接真实检索器**:`ragadapter.SearcherRetriever{Searcher: svcCtx.RAGSearcher, Embedder: embClient, Filter: fenceFilter}` 把生产 `ragsearch.Searcher` 桥成 `rag.Retriever`(`Filter` 用来把检索限定在评测语料,如某测试 user_id/kb_id)。hermetic 单测用 fake `Retriever`。

**数据集**:每条用例的 `expect` 带 golden 标注与阈值:
| 键 | 含义 |
|---|---|
| `relevant_ids` | golden 相关文档/chunk 身份数组(缺失则所有指标跳过) |
| `match_on` | `doc`(默认,按 `Source.SourceID`)或 `chunk`(按 chunk id) |
| `k` | 截断的 top-k(覆盖 runner 的 TopK) |
| `recall_min`/`precision_min`/`hit_min`/`mrr_min`/`ndcg_min` | 各指标通过阈值(缺省 0=只记录不 gate) |

**指标**(确定性、无 LLM,均归一化到 0..1):`RecallAtK`、`PrecisionAtK`、`HitRate@k`、
`MRR`(首个相关命中的倒数排名)、`NDCG@k`(二值相关度归一化折损累计增益)。**要让回归门禁
抓到检索质量跌落**,数据集需为关键用例设 `*_min` 阈值——指标跌破阈值即 pass→fail,`Diff` 才捕获。

**门禁**:`go test ./internal/eval/rag/ -tags eval -run TestRAGGate -v`(参考 `rag_gate_test.go`)。

**生成层忠实度**(答案是否被检索上下文支撑/答案正确性)不在本包:RAG 经 `knowledge_search`
工具交付,端到端质量由 agent 评测(agent + 该工具)承接。

### 真实数据评测(-tags ragreal)

不止 fake:`realdata_test.go` 把**仓库自身的 7 篇真实中文技术文档**灌入**真实 Qdrant +
Elasticsearch**,经生产链路(`ragchunker` 分块 → `ragchunk.Repository` 双写 →
`ragsearch.Searcher` 混合融合)跑自建数据集 `testdata/datasets/cove-docs.json`(12 条真实
查询,golden 用可读文件名标注)。

```bash
docker compose -f deployments/docker-compose.evalstores.yml up -d
# 首次生成基线
EVAL_WRITE_BASELINE=1 go test ./internal/eval/rag/ -tags ragreal -run TestRealDataRetrievalEval -v
# 之后即为回归门禁(对比 testdata/baselines/cove-docs.json)
go test ./internal/eval/rag/ -tags ragreal -run TestRealDataRetrievalEval -v
```

实测基线(7 文档 / 75 chunk / top_k=5):recall\@5 **1.000**、hit\_rate **1.000**、
MRR **1.000**、nDCG **0.9866**、precision\@5 **0.5972**,pass\_rate 100%。阈值取实测值的
保守下界写进数据集,使门禁能抓回归又不抖动。

**嵌入**:默认用 `corpus.HashEmbedder`(字符 bigram 特征哈希 + L2 归一化)—— 无需 API key、
完全可复现,配合 ES 的真实 BM25 通道即可产出有意义的混合排序;要评测真实模型的语义检索,
把真实 `llm.Client` 注入 `Ingester.Embedder` 与 `SearcherRetriever.Embedder` 即可替换。

**两个真实数据才暴露的问题(已修)**:
1. **nDCG 溢出 (0,1]**:doc 级匹配下同一文档有多个 chunk 命中,逐 chunk 计入会让 DCG 重复
   累加同一篇文档,而 IDCG 以 golden 文档数封顶 → 实测出现 nDCG=2.04。现按身份去重后计算,
   与 Recall/Precision 口径一致(回归测试见 `TestNDCGBoundedWithRepeatedDocChunks`)。
2. **排序在多次运行间漂移**:ES 的 BM25 IDF 统计会把"已删除但未段合并"的文档计入,反复
   delete+reindex 会让词项统计逐轮变化,导致同一查询的排名波动(实测某文档从 rank 1 掉到
   rank 3)。现每次评测先删除并重建索引/collection(`resetStores`),连续 3 次运行指标指纹
   完全一致。**在真实检索存储上做评测,务必从干净索引开始。**

## 二期:线上生产评估(暂缓,设计留档)

复用同一 `eval.Scorer` 接口对真实运行(经 harness user-hook 捕获 `react.Result`)打分,
分数经 `harness.Metrics`/OTel seam 发出(`agent_eval_score{scorer}` + span 属性),任意
OTLP 后端(含本地 Langfuse)可视化。不写 Langfuse 专用代码。
```
