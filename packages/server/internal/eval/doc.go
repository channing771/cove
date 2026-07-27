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
