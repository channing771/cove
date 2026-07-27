package rag

import (
	"fmt"

	"github.com/boxify/api-go/internal/eval"
)

// 布尔型断言(负例)用的结果构造助手;数值型指标统一走 metric()。
func okScore(name, detail string) eval.Score {
	return eval.Score{Scorer: name, Value: 1, Passed: true, Detail: detail}
}
func badScore(name, detail string) eval.Score {
	return eval.Score{Scorer: name, Value: 0, Passed: false, Detail: detail}
}
func errScore(name string, err error) eval.Score {
	return eval.Score{Scorer: name, Passed: false, Err: err, ErrText: err.Error()}
}

// MAP 平均精度(Average Precision):在每个命中相关文档的位置上取 P@k 再平均。
//
// 相比 MRR 只看首个相关命中,MAP 对"多个相关文档是否都排在前面"敏感;分母取
// min(|golden|, 检索到的文档数),使结果落在 [0,1]。阈值键 map_min。
func MAP() Scorer {
	return fn{"map", func(c eval.Case, r RetrievalRecord) eval.Score {
		golden, goldenN, matchOn, k, ok := params(c, r)
		if !ok {
			return skip("map")
		}
		flags := r.relevanceFlags(golden, matchOn, k)
		hits, sum := 0, 0.0
		for i, rel := range flags {
			if rel {
				hits++
				sum += float64(hits) / float64(i+1) // 该位置的 P@k
			}
		}
		denom := goldenN
		if len(flags) < denom {
			denom = len(flags)
		}
		value := 0.0
		if denom > 0 {
			value = sum / float64(denom)
		}
		return metric("map", "map_min", value, c)
	}}
}

// F1AtK 是 Precision@k 与 Recall@k 的调和平均。阈值键 f1_min。
//
// 单看召回会奖励"多召回",单看精确会奖励"少而准";F1 在两者间取平衡,适合做总体门禁。
func F1AtK() Scorer {
	return fn{"f1_at_k", func(c eval.Case, r RetrievalRecord) eval.Score {
		golden, goldenN, matchOn, k, ok := params(c, r)
		if !ok {
			return skip("f1_at_k")
		}
		ids := r.identities(matchOn, k)
		inter := 0
		for _, id := range ids {
			if golden[id] {
				inter++
			}
		}
		var precision, recall float64
		if len(ids) > 0 {
			precision = float64(inter) / float64(len(ids))
		}
		if goldenN > 0 {
			recall = float64(inter) / float64(goldenN)
		}
		value := 0.0
		if precision+recall > 0 {
			value = 2 * precision * recall / (precision + recall)
		}
		return metric("f1_at_k", "f1_min", value, c)
	}}
}

// LowRelevanceIs 断言生产的低相关判定与用例期望一致(expect.expect_low_relevance)。
//
// 用于负例:知识库里没有答案的问题,检索器仍会返回"最像"的若干块,但应被标记为低相关,
// 业务层据此决定不使用这些结果。缺该键时跳过。
func LowRelevanceIs() Scorer {
	return fn{"low_relevance", func(c eval.Case, r RetrievalRecord) eval.Score {
		raw, ok := c.Expect["expect_low_relevance"]
		if !ok {
			return skip("low_relevance")
		}
		want, ok := raw.(bool)
		if !ok {
			return errScore("low_relevance", fmt.Errorf("expect_low_relevance 必须是布尔值"))
		}
		if r.Relevance.Low == want {
			return okScore("low_relevance", fmt.Sprintf("low=%v basis=%s", r.Relevance.Low, r.Relevance.Basis))
		}
		return badScore("low_relevance", fmt.Sprintf("low=%v, want %v (basis=%s)", r.Relevance.Low, want, r.Relevance.Basis))
	}}
}

// NoResults 断言检索不返回任何结果(expect.expect_no_results)。
//
// 用于强负例:带过滤条件时越权/越库的查询必须检索为空。缺该键时跳过。
func NoResults() Scorer {
	return fn{"no_results", func(c eval.Case, r RetrievalRecord) eval.Score {
		raw, ok := c.Expect["expect_no_results"]
		if !ok {
			return skip("no_results")
		}
		want, ok := raw.(bool)
		if !ok {
			return errScore("no_results", fmt.Errorf("expect_no_results 必须是布尔值"))
		}
		empty := len(r.Hits) == 0
		if empty == want {
			return okScore("no_results", fmt.Sprintf("hits=%d", len(r.Hits)))
		}
		return badScore("no_results", fmt.Sprintf("hits=%d, want empty=%v", len(r.Hits), want))
	}}
}
