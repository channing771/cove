package rag

import (
	"context"
	"strings"
	"testing"

	"github.com/boxify/api-go/internal/eval"
)

// mkComparison 造两个变体:cand 在指定用例上把 golden 提前/推后。
func mkComparison(t *testing.T, baseOrder, candOrder map[string][]string, ids []string) *Comparison {
	t.Helper()
	cases := make([]eval.Case, 0, len(ids))
	for _, id := range ids {
		cases = append(cases, eval.Case{
			ID: id, Query: id,
			Expect: map[string]any{"relevant_ids": []any{"d1"}, "k": float64(3)},
		})
	}
	ds := &eval.Dataset{Name: "sig", Cases: cases}
	mk := func(order map[string][]string) *RetrievalRunner {
		r := routed{}
		for id, docs := range order {
			r[id] = docHits(docs...)
		}
		return &RetrievalRunner{Retriever: r, TopK: 3}
	}
	cmp, err := (&Comparer{
		Variants: []Variant{
			{Name: "base", Runner: mk(baseOrder)},
			{Name: "cand", Runner: mk(candOrder)},
		},
		Scorers: []Scorer{MRR()},
	}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	return cmp
}

// 候选在每条用例上都严格更好 → 差值区间不跨 0 → 判为显著。
func TestDeltaWithCISignificant(t *testing.T) {
	ids := []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"}
	base, cand := map[string][]string{}, map[string][]string{}
	for _, id := range ids {
		base[id] = []string{"x", "y", "d1"} // golden 在第 3 位 → MRR 0.333
		cand[id] = []string{"d1", "x", "y"} // golden 在第 1 位 → MRR 1.0
	}
	cmp := mkComparison(t, base, cand, ids)
	deltas := cmp.DeltaWithCI("base", "cand", 0.95, 1000, 1)
	if len(deltas) != 1 {
		t.Fatalf("deltas = %d", len(deltas))
	}
	d := deltas[0]
	if d.Delta <= 0 {
		t.Fatalf("delta 应为正: %+v", d)
	}
	if !d.Significant {
		t.Fatalf("每条用例都更好,应判显著: %+v", d)
	}
	if d.CI.Contains(0) {
		t.Fatalf("显著时区间不应跨 0: %+v", d)
	}
	if d.N != len(ids) {
		t.Fatalf("N = %d, want %d", d.N, len(ids))
	}
}

// 有涨有跌、整体差异微小 → 区间跨 0 → 判为不显著(这正是小样本下的常见假象)。
func TestDeltaWithCINotSignificant(t *testing.T) {
	ids := []string{"c1", "c2", "c3", "c4", "c5", "c6"}
	base, cand := map[string][]string{}, map[string][]string{}
	for i, id := range ids {
		if i%2 == 0 {
			base[id] = []string{"d1", "x", "y"}
			cand[id] = []string{"x", "d1", "y"} // 变差
		} else {
			base[id] = []string{"x", "d1", "y"}
			cand[id] = []string{"d1", "x", "y"} // 变好
		}
	}
	cmp := mkComparison(t, base, cand, ids)
	d := cmp.DeltaWithCI("base", "cand", 0.95, 2000, 3)[0]
	if d.Significant {
		t.Fatalf("有涨有跌、净差为零,不应判显著: %+v", d)
	}
	if !d.CI.Contains(0) {
		t.Fatalf("不显著时区间应跨 0: %+v", d)
	}
}

func TestDeltaWithCIUnknownVariant(t *testing.T) {
	cmp := mkComparison(t, map[string][]string{"c1": {"d1"}}, map[string][]string{"c1": {"d1"}}, []string{"c1"})
	if got := cmp.DeltaWithCI("base", "nope", 0.95, 100, 1); got != nil {
		t.Fatal("未知变体应返回 nil")
	}
}

// 对比表应带上区间与显著性标记,让读者一眼看出哪些差异站得住。
func TestWriteComparisonWithCI(t *testing.T) {
	ids := []string{"c1", "c2", "c3", "c4"}
	base, cand := map[string][]string{}, map[string][]string{}
	for _, id := range ids {
		base[id] = []string{"x", "d1"}
		cand[id] = []string{"d1", "x"}
	}
	cmp := mkComparison(t, base, cand, ids)
	var sb strings.Builder
	if err := cmp.WriteDeltaTable(&sb, "base", "cand", 0.95, 500, 2); err != nil {
		t.Fatalf("WriteDeltaTable: %v", err)
	}
	out := sb.String()
	for _, want := range []string{"mrr", "delta", "95%CI", "显著"} {
		if !strings.Contains(out, want) {
			t.Fatalf("表格缺少 %q:\n%s", want, out)
		}
	}
}
