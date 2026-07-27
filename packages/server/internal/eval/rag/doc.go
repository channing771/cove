// Package rag 提供 RAG 模块的检索质量评测,复用 internal/eval 的数据集/报告/回归门禁。
//
// SUT 是检索器(Retriever,生产实现见子包 ragadapter 包装的 ragsearch.Searcher),
// 而非整个 agent。用例经 eval.Case 承载(Expect 里带 golden 文档标注 relevant_ids、
// match_on、k 与各指标阈值),打分器产出标准 IR 指标:Recall@k / Precision@k /
// HitRate@k / MRR / nDCG@k(确定性、无 LLM),外加可选 ContextRelevance(LLM-judge)。
//
// 生成层忠实度(faithfulness/answer-correctness)由现有 agent 评测经 knowledge_search
// 工具承接,不在本包范围。
package rag
