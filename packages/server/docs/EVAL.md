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

**数据集**:每条用例的 `expect` 只存 golden 等**客观标注**(阈值另见"阈值剖面"一节):
| 键 | 含义 |
|---|---|
| `relevant_ids` | golden 相关文档/chunk 身份数组(缺失则所有指标跳过) |
| `match_on` | `doc`(默认,按 `Source.SourceID`)、`chunk`(按 chunk id)或 `name`(按文档名,便于可读标注) |
| `k` | 截断的 top-k(覆盖 runner 的 TopK) |
| `expect_low_relevance` / `expect_no_results` | 负例断言(见下) |

**指标**(确定性、无 LLM,均归一化到 0..1):

| 打分器 | 含义 | 阈值键 |
|---|---|---|
| `RecallAtK` | 召回的 golden 占比 | `recall_min` |
| `PrecisionAtK` | 检索结果中相关的占比(分母为**检索到的不同文档数**,适配可变长结果) | `precision_min` |
| `HitRate` | 前 k 是否至少命中一个 golden | `hit_min` |
| `MRR` | 首个相关命中的倒数排名 | `mrr_min` |
| `NDCG` | 二值相关度的归一化折损累计增益 | `ndcg_min` |
| `MAP` | 各相关命中位置 P@k 的平均——对"多个相关文档是否都靠前"敏感 | `map_min` |
| `F1AtK` | Precision/Recall 的调和平均,适合做总体门禁 | `f1_min` |

**要让回归门禁抓到检索质量跌落**,需为关键用例设 `*_min` 阈值(放在阈值剖面里)——指标
跌破阈值即 pass→fail,`Diff` 才捕获。阈值应取**实测绿色运行的保守下界**,而非拍脑袋。

**负例(库里没有答案的问题)**:`LowRelevanceIs`(`expect_low_relevance`)断言生产的低相关
判定;`NoResults`(`expect_no_results`)断言检索为空(如越权/越库必须查不到)。

### 检索配置对比(RAG 调参)

评测的主用途之一是回答"向量权重调高有没有提升""开重排值不值""top_k 取多少"。`Comparer`
在**同一数据集、同一组打分器**下横向跑多个配置:

```go
cmp, _ := (&rag.Comparer{
    Variants: []rag.Variant{
        {Name: "balanced", Runner: runnerWith(0.6, 0.4)},
        {Name: "bm25-heavy", Runner: runnerWith(0.1, 0.9)},
    },
    Scorers: []rag.Scorer{rag.RecallAtK(), rag.PrecisionAtK(), rag.MRR(), rag.NDCG(), rag.MAP(), rag.F1AtK()},
}).Run(ctx, ds)

cmp.WriteTable(os.Stdout)             // 配置 × 指标 对比表
cmp.Best("ndcg")                      // 该指标下最优配置名
cmp.Delta("balanced", "bm25-heavy")   // 逐指标均值差(正=候选更好)
```

真实语料实测(见 `TestRealDataWeightComparison`,**词形 HashEmbedder、n=12**),
bm25-heavy 相对 balanced,**带配对自助 95% 置信区间**:

| 指标 | balanced | bm25-heavy | delta | 95%CI | 显著? |
|---|---|---|---|---|---|
| precision@5 | 0.6389 | 0.7361 | +0.0972 | [+0.0000, +0.2222] | ✗ |
| f1 | 0.7167 | 0.7778 | +0.0611 | [−0.0361, +0.1694] | ✗ |
| recall@5 | 0.9583 | 0.9167 | −0.0417 | [−0.1250, +0.0000] | ✗ |
| nDCG | 0.9933 | 0.9678 | −0.0255 | [−0.0766, +0.0000] | ✗ |
| MAP | 0.9861 | 0.9583 | −0.0278 | [−0.0833, +0.0000] | ✗ |
| MRR | 1.0000 | 1.0000 | +0.0000 | [+0.0000, +0.0000] | ✗ |

**结论:0/6 项差异达到统计显著。** 在 12 条用例下,这些均值差与抽样噪声无法区分——
一条用例的排名变化就能造成 ±0.08 的均值波动。**不能据此断言"提高 BM25 权重更准但更漏"**
(本文档早前版本正是这样断言的,属于过度解读,现已更正)。

要让配置对比真正支撑调参决策,必须先把数据集扩到足以产生统计效力的规模。

**显著性判定**:`Comparison.DeltaWithCI` 用**配对**自助重采样(两个配置跑同一批用例,配对可
消掉用例难度差异,统计效力更高),`WriteDeltaTable` 输出带区间的对比表。判据是区间在容差外
完整位于 0 的一侧(`Interval.ExcludesZero`)——离散指标 + 小样本时区间边界常**恰好**落在 0,
浮点残差会把"边界贴着 0"误判成显著,故必须带容差(实测中 precision@5 就踩中过这个陷阱)。

### 按标签分组

数据集用例的 `tags` 可用 `report.ByTag()` 聚合,定位哪一类查询弱(一条用例多标签会计入每个标签)。

**门禁**:`go test ./internal/eval/rag/ -tags eval -run TestRAGGate -v`(参考 `rag_gate_test.go`)。

### 生成层评测(faithfulness / 有无幻觉)

检索指标只能证明"找对了资料",证明不了"答得对、没幻觉"。生成层由 `GenerationRunner`
(检索 → 生成)+ `GenScorer` 承接,同样产出 `eval.Report`:

| 打分器 | 判什么 | 期望键 |
|---|---|---|
| `Faithfulness` | 答案是否**完全由检索上下文支撑**(有无幻觉)——RAG 生成层的底线 | 无(恒执行) |
| `AnswerRelevance` | 是否切题回应了问题(与事实对错正交) | 无 |
| `AnswerCorrectness` | 对照参考答案是否正确完整 | `reference` |
| `AnswerContains` | 关键事实子串硬断言,**无需 LLM、零成本** | `answer_contains` |

`GenerationRecord` 内嵌 `RetrievalRecord`,使检索与生成指标能在同一次运行里一起看——
检索没召回到证据时,生成不忠实往往是检索的锅,分开跑就归因不了。

真实端到端(见 `TestRealDataGenerationEval`,GLM 检索 + `glm-4-flash` 生成与评审,
10 条用例含 2 条"资料里没有"的问题):**faithfulness 10/10、answer_relevance 10/10、
answer_correctness 10/10,pass_rate 100%**。

> **一个必须避开的陷阱**:首版 rubric 把"根据已有资料无法回答"判成不忠实(score=0)——
> 而这恰恰是**最理想的无幻觉行为**。照这样的评测调优,会把模型往编造答案的方向推。
> 现已在代码层面确定性识别弃答(`GenJudge.AbstentionMarkers`,不作断言即无幻觉,直接满分,
> 也省掉一次评审调用),并在两个 rubric 里补了对应条款。修复后 faithfulness 由 8/10 → 10/10。

**注意:生成层不可复现**。评审与生成都走 LLM,同一输入的评分会小幅波动(实测
answer_correctness 0.85↔0.89),因此生成层用**阈值门禁**(如 faithfulness 通过率 ≥80%)
而非精确基线比对;确定性的 `AnswerContains` 可用于关键事实的硬门禁。

### 真实数据评测(-tags ragreal)

不止 fake:`realdata_test.go` 把**仓库自身的 26 篇真实中文技术文档**(416KB / 451 chunk)
灌入**真实 Qdrant + Elasticsearch**,经生产链路(`ragchunker` 分块 → `ragchunk.Repository`
双写 → `ragsearch.Searcher` 混合融合)跑自建数据集 `testdata/datasets/cove-docs.json`
(**47 条**真实查询,golden 用可读文件名标注,覆盖网关/MCP/架构/部署/codegen/约定/rerank/
工具技能等主题,并含多跳、短查询、中英混合、口语化等查询类型)。

```bash
docker compose -f deployments/docker-compose.evalstores.yml up -d
# 首次生成基线
EVAL_WRITE_BASELINE=1 go test ./internal/eval/rag/ -tags ragreal -run TestRealDataRetrievalEval -v
# 之后即为回归门禁(对比 testdata/baselines/cove-docs.json)
go test ./internal/eval/rag/ -tags ragreal -run TestRealDataRetrievalEval -v
```

实测基线见下方"实测对比"表(两套向量模型各一份基线,pass_rate 均 100%)。阈值取实测值的
保守下界,放在**阈值剖面**里(见"阈值剖面"一节),使门禁能抓回归又不抖动。

#### 向量模型:GLM embedding-3(真实语义)或 HashEmbedder(离线)

评测按环境变量自动选择向量模型,**摄入与检索始终用同一个**(类型上由
`corpus.BatchEmbedderQuerier` 固化——两侧模型不一致会让向量落在不同语义空间,检索失去意义):

```bash
# 真实语义向量:GLM embedding-3
export GLM_API_KEY=<你的智谱 key>          # 或 ZHIPU_API_KEY
go test ./internal/eval/rag/ -tags ragreal -run TestRealDataRetrievalEval -v

# 不设 key → 自动回退确定性 HashEmbedder(词形,离线可复现)
```

| env | 默认 | 说明 |
|---|---|---|
| `GLM_API_KEY` / `ZHIPU_API_KEY` | 空 | 有值即启用 GLM,否则回退 hash |
| `GLM_EMBEDDING_MODEL` | `embedding-3` | 向量模型名 |
| `GLM_EMBEDDING_DIM` | `1024` | embedding-3 支持 256/512/1024/2048 |
| `GLM_BASE_URL` | `https://open.bigmodel.cn/api/paas/v4` | 可指向兼容网关 |

GLM 的 `/embeddings` 与 OpenAI 同构,故直接复用仓库既有的 OpenAI 兼容客户端
(`zhipu` 本就走 `OpenAICompatibleFactory`),**未新增 provider**;`corpus.ClientEmbedder`
负责把 `llm.Client` 适配成评测所需的批量+单条嵌入接口,并按 `GLMEmbeddingBatchSize`
切批(真实向量服务对单请求输入条数有上限)。

**两种向量模型各自隔离**:collection/index、基线、**阈值剖面**均按嵌入器区分
(`cove_eval_chunks` / `cove_eval_chunks_glm_<dim>`,`baselines/cove-docs{,-glm}.json`,
`thresholds/{hash,glm}.json`),避免维度不匹配与指标互相污染。

#### 实测对比:GLM embedding-3 vs 词形 HashEmbedder

同一冻结语料(26 篇 / 451 chunk)、同一批 **47 条** golden 标注查询、同一混合检索链路,
top_k=5,**配对自助 95% 置信区间**(见 `TestRealDataEmbedderComparison`):

| 指标 | hash(词形) | **GLM embedding-3** | delta | 95%CI | 显著? |
|---|---|---|---|---|---|
| recall@5 | 0.7323 | **0.8954** | +0.1631 | [+0.053, +0.277] | ✓ |
| precision@5 | 0.4504 | **0.6379** | +0.1876 | [+0.098, +0.276] | ✓ |
| F1 | 0.5211 | **0.7047** | +0.1836 | [+0.104, +0.262] | ✓ |
| MRR | 0.7057 | **0.8901** | +0.1844 | [+0.089, +0.291] | ✓ |
| nDCG | 0.6968 | **0.8757** | +0.1789 | [+0.081, +0.277] | ✓ |
| MAP | 0.6613 | **0.8533** | +0.1919 | [+0.096, +0.294] | ✓ |
| hit_rate | 0.8085 | **0.9362** | +0.1277 | [+0.021, +0.234] | ✓ |

**7/7 项提升均达到统计显著**——这是可用于决策的结论。对比此前 12 条用例时权重对比
0/6 显著,说明**数据集规模才是评测能否支撑决策的前提**。

**负例判定(域外问题)—— 语义向量才做得到**:

| 向量模型 | 域内 max_score | 域外 max_score | 是否可分 |
|---|---|---|---|
| hash(词形) | [0.2094, 0.4503] | [0.2306, 0.2464] | ✗ 区间重叠,无可分阈值 |
| **GLM embedding-3** | **[0.5957, 0.7852]** | **[0.3614, 0.3897]** | ✓ 间隔 0.206,阈值取 0.49 |

故 `TestRealDataNegativeGate` 仅在 GLM 下执行(hash 下自动 skip 而非给假绿),
实测 **15/15 通过**(3 条域外正确判低相关 + 12 条域内无误判)。
校准工具:`TestRealDataNegativeCalibration` 打印两侧分数区间并给出建议阈值。

### 阈值剖面:把"客观事实"与"对配置的期望"分开

数据集只存 **golden 标注等客观事实**(`relevant_ids`/`match_on`/`k`);各指标的 `*_min`
阈值放在 `testdata/thresholds/<profile>.json`,载入时经 `eval.ThresholdProfile.ApplyTo` 合并。

理由:golden 是"哪些文档确实相关"的事实,换向量模型不会变;阈值是"该配置应达到的水平",
换配置必然变。混在一起会导致换模型就要改 golden。拆开后一份数据集可配多套剖面
(词形一套、语义一套),各自门禁互不牵连——实测正是如此:同一份 golden 下 hash 的
recall 只有 0.9583 而 GLM 是 1.0000,两者各按自己的剖面 100% 通过。

剖面中出现数据集里不存在的 case_id 会**报错**,挡住改名/拼写导致的"阈值静默失效"。

- `HashEmbedder`:字符 bigram 特征哈希 + L2 归一化,无需 API key、完全可复现,配合 ES 的
  真实 BM25 通道即可产出有意义的混合排序;但只有词形信号,无法分辨域外问题(见下方负例局限)。
- `GLM embedding-3`:真实语义向量,联网、有调用成本,指标随模型版本变化。

**语料必须冻结**:语料是 `testdata/corpus/` 下的**快照**,不是直接读活文档。因为语料里含
`docs/EVAL.md`,而记录这套评测本身就会改它——活文档一变,chunk 与检索结果随之变化,基线
悄悄失效(实测 nDCG 0.9866→0.9933)。更新语料时须同步刷新快照与基线。

**负例门禁**:域外问题存放在 `testdata/datasets/cove-docs-negatives.json`。词形
`HashEmbedder` 下域内/域外分数区间重叠、无可分阈值(故不启用,不给假绿);换成 GLM
embedding-3 后间隔达 0.206,门禁已启用并 15/15 通过。数据见上文"负例判定"表。

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
