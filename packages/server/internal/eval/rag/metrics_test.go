package rag

import (
	"context"
	"testing"

	"github.com/boxify/api-go/internal/eval"
)

// golden={d1,d2},命中顺序 [d1,d3,d2,d4,d5]
// AP = (P@1*1 + P@3*1)/2 = (1/1 + 2/3)/2 = 0.8333
func TestMAP(t *testing.T) {
	c, rec := scenario()
	got := MAP().Score(context.Background(), c, rec)
	if !near(got.Value, 0.83333) {
		t.Fatalf("map = %v, want ≈0.8333", got.Value)
	}
}

// 相关文档全在最前时 AP=1。
func TestMAPPerfect(t *testing.T) {
	rec := RetrievalRecord{Hits: docHits("d1", "d2", "d9"), RequestedK: 5}
	c := eval.Case{Expect: map[string]any{"relevant_ids": []any{"d1", "d2"}}}
	if got := MAP().Score(context.Background(), c, rec); !near(got.Value, 1.0) {
		t.Fatalf("map = %v, want 1.0", got.Value)
	}
}

// recall=1.0, precision=0.4 → F1 = 2*1*0.4/1.4 = 0.5714
func TestF1AtK(t *testing.T) {
	c, rec := scenario()
	got := F1AtK().Score(context.Background(), c, rec)
	if !near(got.Value, 0.57143) {
		t.Fatalf("f1 = %v, want ≈0.5714", got.Value)
	}
}

func TestF1ZeroWhenNothingRelevant(t *testing.T) {
	rec := RetrievalRecord{Hits: docHits("x1", "x2"), RequestedK: 5}
	c := eval.Case{Expect: map[string]any{"relevant_ids": []any{"d1"}}}
	if got := F1AtK().Score(context.Background(), c, rec); got.Value != 0 {
		t.Fatalf("f1 = %v, want 0", got.Value)
	}
}

func TestNewMetricsSkipWithoutGolden(t *testing.T) {
	rec := RetrievalRecord{Hits: docHits("d1"), RequestedK: 5}
	for _, s := range []Scorer{MAP(), F1AtK()} {
		if got := s.Score(context.Background(), eval.Case{}, rec); !got.Skipped {
			t.Fatalf("%s should skip without relevant_ids", s.Name())
		}
	}
}

// 负例:库里没有答案的问题,应被生产的低相关判定标记。
func TestLowRelevanceScorer(t *testing.T) {
	ctx := context.Background()
	c := eval.Case{Expect: map[string]any{"expect_low_relevance": true}}

	flagged := RetrievalRecord{Hits: docHits("d1"), Relevance: Relevance{Low: true, Basis: "vector"}}
	if got := LowRelevanceIs().Score(ctx, c, flagged); !got.Passed {
		t.Fatalf("低相关被正确标记应通过: %+v", got)
	}
	notFlagged := RetrievalRecord{Hits: docHits("d1"), Relevance: Relevance{Low: false}}
	if got := LowRelevanceIs().Score(ctx, c, notFlagged); got.Passed {
		t.Fatal("未标记低相关应失败(负例被当成有效检索)")
	}
	// 正例:期望不是低相关
	pos := eval.Case{Expect: map[string]any{"expect_low_relevance": false}}
	if got := LowRelevanceIs().Score(ctx, pos, notFlagged); !got.Passed {
		t.Fatalf("正例未标记低相关应通过: %+v", got)
	}
	// 无该期望键 → 跳过
	if got := LowRelevanceIs().Score(ctx, eval.Case{}, flagged); !got.Skipped {
		t.Fatal("无 expect_low_relevance 应跳过")
	}
}

// 负例还应能断言"不要检索出任何文档"。
func TestExpectNoResults(t *testing.T) {
	ctx := context.Background()
	c := eval.Case{Expect: map[string]any{"expect_no_results": true}}
	if got := NoResults().Score(ctx, c, RetrievalRecord{}); !got.Passed {
		t.Fatal("空结果应通过")
	}
	if got := NoResults().Score(ctx, c, RetrievalRecord{Hits: docHits("d1")}); got.Passed {
		t.Fatal("有结果应失败")
	}
	if got := NoResults().Score(ctx, eval.Case{}, RetrievalRecord{}); !got.Skipped {
		t.Fatal("无该键应跳过")
	}
}
