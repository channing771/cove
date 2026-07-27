package rag

import (
	"context"
	"fmt"
	"math"

	"github.com/boxify/api-go/internal/eval"
)

// Scorer 是 RAG 检索打分器,产出 eval.Score 以复用 eval 的报告/回归门禁。
type Scorer interface {
	Name() string
	Score(ctx context.Context, c eval.Case, r RetrievalRecord) eval.Score
}

type fn struct {
	name string
	f    func(c eval.Case, r RetrievalRecord) eval.Score
}

func (s fn) Name() string { return s.name }
func (s fn) Score(_ context.Context, c eval.Case, r RetrievalRecord) eval.Score {
	return s.f(c, r)
}

// params 解析用例的 golden 标注与检索参数;无 relevant_ids 时 ok=false(打分器应跳过)。
func params(c eval.Case, r RetrievalRecord) (golden map[string]bool, goldenN int, matchOn string, k int, ok bool) {
	ids, has := eval.ExpectStrings(c, "relevant_ids")
	if !has || len(ids) == 0 {
		return nil, 0, "", 0, false
	}
	golden = make(map[string]bool, len(ids))
	for _, id := range ids {
		golden[id] = true
	}
	matchOn, _ = eval.ExpectString(c, "match_on")
	if matchOn == "" {
		matchOn = "doc"
	}
	k = r.RequestedK
	if v, ok2 := eval.ExpectInt(c, "k"); ok2 && v > 0 {
		k = v
	}
	if k <= 0 {
		k = len(r.Hits)
	}
	return golden, len(golden), matchOn, k, true
}

func skip(name string) eval.Score {
	return eval.Score{Scorer: name, Skipped: true, Passed: true, Detail: "skipped: no relevant_ids"}
}

// metric 按阈值键(缺省阈值 0)判定通过并组装 Score。value 已在 [0,1]。
func metric(name, thrKey string, value float64, c eval.Case) eval.Score {
	thr, _ := eval.ExpectFloat(c, thrKey)
	return eval.Score{
		Scorer: name,
		Value:  value,
		Passed: value >= thr,
		Detail: fmt.Sprintf("%.4f (min %.4f)", value, thr),
	}
}

// RecallAtK 命中 golden 的比例:|检索身份 ∩ golden| / |golden|。阈值键 recall_min。
func RecallAtK() Scorer {
	return fn{"recall_at_k", func(c eval.Case, r RetrievalRecord) eval.Score {
		golden, goldenN, matchOn, k, ok := params(c, r)
		if !ok {
			return skip("recall_at_k")
		}
		inter := 0
		for _, id := range r.identities(matchOn, k) {
			if golden[id] {
				inter++
			}
		}
		return metric("recall_at_k", "recall_min", float64(inter)/float64(goldenN), c)
	}}
}

// PrecisionAtK 检索结果中相关的比例:|检索身份 ∩ golden| / |检索身份|。阈值键 precision_min。
func PrecisionAtK() Scorer {
	return fn{"precision_at_k", func(c eval.Case, r RetrievalRecord) eval.Score {
		golden, _, matchOn, k, ok := params(c, r)
		if !ok {
			return skip("precision_at_k")
		}
		ids := r.identities(matchOn, k)
		if len(ids) == 0 {
			return metric("precision_at_k", "precision_min", 0, c)
		}
		inter := 0
		for _, id := range ids {
			if golden[id] {
				inter++
			}
		}
		return metric("precision_at_k", "precision_min", float64(inter)/float64(len(ids)), c)
	}}
}

// HitRate 前 k 条是否至少命中一个 golden(1 或 0)。阈值键 hit_min(设 1 则要求必中)。
func HitRate() Scorer {
	return fn{"hit_rate", func(c eval.Case, r RetrievalRecord) eval.Score {
		golden, _, matchOn, k, ok := params(c, r)
		if !ok {
			return skip("hit_rate")
		}
		value := 0.0
		for _, hit := range r.relevanceFlags(golden, matchOn, k) {
			if hit {
				value = 1
				break
			}
		}
		return metric("hit_rate", "hit_min", value, c)
	}}
}

// MRR 首个相关命中的倒数排名:1/rank(无相关命中为 0)。阈值键 mrr_min。
func MRR() Scorer {
	return fn{"mrr", func(c eval.Case, r RetrievalRecord) eval.Score {
		golden, _, matchOn, k, ok := params(c, r)
		if !ok {
			return skip("mrr")
		}
		value := 0.0
		for i, hit := range r.relevanceFlags(golden, matchOn, k) {
			if hit {
				value = 1.0 / float64(i+1)
				break
			}
		}
		return metric("mrr", "mrr_min", value, c)
	}}
}

// NDCG 二值相关度的归一化折损累计增益(nDCG@k)。阈值键 ndcg_min。
func NDCG() Scorer {
	return fn{"ndcg", func(c eval.Case, r RetrievalRecord) eval.Score {
		golden, goldenN, matchOn, k, ok := params(c, r)
		if !ok {
			return skip("ndcg")
		}
		flags := r.relevanceFlags(golden, matchOn, k)
		dcg := 0.0
		for i, hit := range flags {
			if hit {
				dcg += 1.0 / math.Log2(float64(i+2))
			}
		}
		ideal := goldenN
		if len(flags) < ideal {
			ideal = len(flags)
		}
		idcg := 0.0
		for i := 0; i < ideal; i++ {
			idcg += 1.0 / math.Log2(float64(i+2))
		}
		value := 0.0
		if idcg > 0 {
			value = dcg / idcg
		}
		return metric("ndcg", "ndcg_min", value, c)
	}}
}
