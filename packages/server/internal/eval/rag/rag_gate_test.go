//go:build eval

package rag

import (
	"context"
	"testing"

	"github.com/boxify/api-go/internal/eval"
)

// routedRetriever 按 query 返回预置命中,hermetic 驱动整条评测链路。
type routedRetriever map[string][]RetrievedHit

func (r routedRetriever) Retrieve(_ context.Context, query string, _ int) ([]RetrievedHit, error) {
	return r[query], nil
}

func TestRAGGate(t *testing.T) {
	ds, err := eval.LoadDataset("testdata/datasets/retrieval.json")
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	ret := routedRetriever{
		"什么是向量数据库": docHits("doc-vec", "doc-x", "doc-embed"),
		"重排序如何工作":   docHits("doc-rerank", "doc-y"),
	}
	e := &Evaluator{
		Runner:  &RetrievalRunner{Retriever: ret, TopK: 5},
		Scorers: []Scorer{RecallAtK(), PrecisionAtK(), HitRate(), MRR(), NDCG()},
	}
	rep, err := e.Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("eval run: %v", err)
	}
	_ = rep.WriteTable(gateWriter{t})
	baseline, err := eval.LoadReport("testdata/baselines/retrieval.json")
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	if err := rep.Diff(baseline).Err(); err != nil {
		t.Error(err)
	}
	if rep.PassRate != 1 {
		t.Fatalf("pass rate = %v, want 1", rep.PassRate)
	}
}

type gateWriter struct{ t *testing.T }

func (w gateWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }
