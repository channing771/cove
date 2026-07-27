package rag

import (
	"context"
	"math"
	"testing"

	"github.com/boxify/api-go/internal/eval"
)

// 命中(doc 级):[d1, d3, d2, d4, d5];golden = {d1, d2}。
func scenario() (eval.Case, RetrievalRecord) {
	rec := RetrievalRecord{Hits: docHits("d1", "d3", "d2", "d4", "d5"), RequestedK: 5}
	c := eval.Case{Expect: map[string]any{"relevant_ids": []any{"d1", "d2"}}}
	return c, rec
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-4 }

func TestRecallPrecisionHit(t *testing.T) {
	c, rec := scenario()
	if s := RecallAtK().Score(context.Background(), c, rec); !near(s.Value, 1.0) {
		t.Fatalf("recall = %v, want 1.0 (%+v)", s.Value, s)
	}
	if s := PrecisionAtK().Score(context.Background(), c, rec); !near(s.Value, 0.4) {
		t.Fatalf("precision = %v, want 0.4", s.Value)
	}
	if s := HitRate().Score(context.Background(), c, rec); s.Value != 1 {
		t.Fatalf("hit = %v, want 1", s.Value)
	}
}

func TestMRRAndNDCG(t *testing.T) {
	c, rec := scenario()
	if s := MRR().Score(context.Background(), c, rec); !near(s.Value, 1.0) {
		t.Fatalf("mrr = %v, want 1.0 (first hit at rank 1)", s.Value)
	}
	// flags [1,0,1,0,0]: dcg = 1/log2(2)+1/log2(4)=1.5; idcg = 1/log2(2)+1/log2(3)=1.6309; ndcg≈0.9197
	got := NDCG().Score(context.Background(), c, rec)
	if !near(got.Value, 0.91972) {
		t.Fatalf("ndcg = %v, want ≈0.9197", got.Value)
	}
}

func TestThresholdGating(t *testing.T) {
	c, rec := scenario()
	c.Expect["recall_min"] = float64(1.0)
	if s := RecallAtK().Score(context.Background(), c, rec); !s.Passed {
		t.Fatalf("recall 1.0 should meet min 1.0: %+v", s)
	}
	c.Expect["precision_min"] = float64(0.5)
	if s := PrecisionAtK().Score(context.Background(), c, rec); s.Passed {
		t.Fatal("precision 0.4 should fail min 0.5")
	}
}

func TestSkipWithoutGolden(t *testing.T) {
	rec := RetrievalRecord{Hits: docHits("d1"), RequestedK: 5}
	for _, s := range []Scorer{RecallAtK(), PrecisionAtK(), HitRate(), MRR(), NDCG()} {
		if got := s.Score(context.Background(), eval.Case{}, rec); !got.Skipped {
			t.Fatalf("%s should skip without relevant_ids: %+v", s.Name(), got)
		}
	}
}

func TestMatchOnChunk(t *testing.T) {
	rec := RetrievalRecord{Hits: docHits("d1", "d2"), RequestedK: 5}
	// golden 用 chunk id;match_on=chunk
	c := eval.Case{Expect: map[string]any{"relevant_ids": []any{"d1-c"}, "match_on": "chunk"}}
	if s := RecallAtK().Score(context.Background(), c, rec); !near(s.Value, 1.0) {
		t.Fatalf("chunk-level recall = %v, want 1.0", s.Value)
	}
}
