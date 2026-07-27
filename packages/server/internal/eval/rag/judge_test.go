package rag

import (
	"context"
	"errors"
	"testing"

	"github.com/boxify/api-go/internal/core/llm"
	"github.com/boxify/api-go/internal/eval"
)

type judgeClient struct{ out string }

func (j judgeClient) Invoke(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (string, error) {
	return j.out, nil
}
func (judgeClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return nil, errors.New("n/a")
}
func (judgeClient) Stream(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan string, error) {
	return nil, errors.New("n/a")
}
func (judgeClient) Embed(ctx context.Context, t []string, d int, o ...llm.EmbeddingOption) ([][]float64, error) {
	return nil, errors.New("n/a")
}
func (judgeClient) EmbedOne(ctx context.Context, t string, d int) ([]float64, error) {
	return nil, errors.New("n/a")
}

func TestContextRelevanceParses(t *testing.T) {
	rec := RetrievalRecord{Hits: docHits("d1", "d2"), RequestedK: 5}
	j := ContextRelevance{Client: judgeClient{out: `裁定 {"score":0.7,"pass":true,"reason":"相关"}`}}
	s := j.Score(context.Background(), eval.Case{Query: "q"}, rec)
	if s.Err != nil || !s.Passed || s.Value != 0.7 {
		t.Fatalf("relevance = %+v", s)
	}
}

func TestContextRelevanceNilClientSkips(t *testing.T) {
	if !(ContextRelevance{}).Score(context.Background(), eval.Case{}, RetrievalRecord{}).Skipped {
		t.Fatal("nil client should skip")
	}
}

func TestContextRelevanceEmptyContextFails(t *testing.T) {
	j := ContextRelevance{Client: judgeClient{out: `{"score":1}`}}
	s := j.Score(context.Background(), eval.Case{Query: "q"}, RetrievalRecord{})
	if s.Passed {
		t.Fatal("no retrieved context should fail, not pass")
	}
}
