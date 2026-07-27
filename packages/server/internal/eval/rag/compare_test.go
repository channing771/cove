package rag

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/boxify/api-go/internal/eval"
)

// 两个变体:A 把 golden 排首位,B 排末位 —— 对比应能量化出 A 更好。
func compareFixture() (*eval.Dataset, []Variant) {
	ds := &eval.Dataset{Name: "cmp", Cases: []eval.Case{{
		ID:    "c1",
		Query: "q",
		Expect: map[string]any{
			"relevant_ids": []any{"d1"},
			"k":            float64(3),
		},
	}}}
	good := routed{"q": docHits("d1", "d2", "d3")}
	bad := routed{"q": docHits("d2", "d3", "d1")}
	return ds, []Variant{
		{Name: "good", Runner: &RetrievalRunner{Retriever: good, TopK: 3}},
		{Name: "bad", Runner: &RetrievalRunner{Retriever: bad, TopK: 3}},
	}
}

type routed map[string][]RetrievedHit

func (r routed) Retrieve(_ context.Context, query string, _ int) ([]RetrievedHit, error) {
	return r[query], nil
}

func TestComparerRunsAllVariants(t *testing.T) {
	ds, variants := compareFixture()
	c := &Comparer{Variants: variants, Scorers: []Scorer{RecallAtK(), MRR(), NDCG()}}
	cmp, err := c.Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(cmp.Variants) != 2 {
		t.Fatalf("variants = %d, want 2", len(cmp.Variants))
	}
	good := cmp.Report("good")
	bad := cmp.Report("bad")
	if good == nil || bad == nil {
		t.Fatal("both variant reports must exist")
	}
	if good.Scorers["mrr"].MeanValue <= bad.Scorers["mrr"].MeanValue {
		t.Fatalf("good MRR (%v) 应高于 bad (%v)", good.Scorers["mrr"].MeanValue, bad.Scorers["mrr"].MeanValue)
	}
	// 两者 recall 都是 1(golden 都在 top3 内),对比要能体现"排序变差但召回不变"
	if !near(good.Scorers["recall_at_k"].MeanValue, 1) || !near(bad.Scorers["recall_at_k"].MeanValue, 1) {
		t.Fatal("两个变体的 recall 都应为 1")
	}
}

func TestComparerBestAndDelta(t *testing.T) {
	ds, variants := compareFixture()
	cmp, err := (&Comparer{Variants: variants, Scorers: []Scorer{MRR(), NDCG()}}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if best := cmp.Best("mrr"); best != "good" {
		t.Fatalf("best by mrr = %q, want good", best)
	}
	deltas := cmp.Delta("bad", "good")
	if len(deltas) == 0 {
		t.Fatal("delta 应给出逐指标差值")
	}
	var mrrDelta float64
	found := false
	for _, d := range deltas {
		if d.Scorer == "mrr" {
			mrrDelta, found = d.Delta, true
		}
	}
	if !found || mrrDelta <= 0 {
		t.Fatalf("good 相对 bad 的 mrr delta 应为正: %v (found=%v)", mrrDelta, found)
	}
}

func TestComparisonWriteTable(t *testing.T) {
	ds, variants := compareFixture()
	cmp, _ := (&Comparer{Variants: variants, Scorers: []Scorer{MRR()}}).Run(context.Background(), ds)
	var buf bytes.Buffer
	if err := cmp.WriteTable(&buf); err != nil {
		t.Fatalf("WriteTable: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"good", "bad", "mrr"} {
		if !strings.Contains(out, want) {
			t.Fatalf("表格应含 %q:\n%s", want, out)
		}
	}
}

func TestComparerRejectsEmpty(t *testing.T) {
	if _, err := (&Comparer{}).Run(context.Background(), &eval.Dataset{Name: "x"}); err == nil {
		t.Fatal("无变体应报错")
	}
}
