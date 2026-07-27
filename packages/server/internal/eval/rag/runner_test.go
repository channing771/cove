package rag

import (
	"context"
	"testing"

	"github.com/boxify/api-go/internal/eval"
)

func TestRetrievalRunnerUsesExpectK(t *testing.T) {
	fr := &fakeRetriever{hits: docHits("d1", "d2")}
	r := &RetrievalRunner{Retriever: fr, TopK: 5}
	rec, err := r.Run(context.Background(), eval.Case{ID: "c", Query: "q", Expect: map[string]any{"k": float64(3)}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fr.lastK != 3 {
		t.Fatalf("topK = %d, want 3 (Expect.k override)", fr.lastK)
	}
	if fr.lastQuery != "q" {
		t.Fatalf("query = %q", fr.lastQuery)
	}
	if rec.RequestedK != 3 || len(rec.Hits) != 2 || rec.Latency < 0 {
		t.Fatalf("record = %+v", rec)
	}
}

func TestRetrievalRunnerDefaultTopK(t *testing.T) {
	fr := &fakeRetriever{hits: docHits("d1")}
	rec, _ := (&RetrievalRunner{Retriever: fr}).Run(context.Background(), eval.Case{ID: "c", Query: "q"})
	if fr.lastK != 5 || rec.RequestedK != 5 {
		t.Fatalf("default topK = %d, want 5", fr.lastK)
	}
}

func TestRetrievalRunnerNilRetriever(t *testing.T) {
	if _, err := (&RetrievalRunner{}).Run(context.Background(), eval.Case{ID: "c"}); err == nil {
		t.Fatal("nil retriever should error")
	}
}
