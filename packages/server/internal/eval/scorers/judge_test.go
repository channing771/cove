package scorers

import (
	"context"
	"errors"
	"testing"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
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

func TestLLMJudgePassAndParse(t *testing.T) {
	j := LLMJudge{Client: judgeClient{out: `这是评价 {"score":0.9,"pass":true,"reason":"good"}`}}
	r := eval.RunRecord{Result: &corereact.Result{Answer: "42"}}
	s := j.Score(context.Background(), eval.Case{Query: "q"}, r)
	if !s.Passed || s.Value != 0.9 {
		t.Fatalf("judge = %+v", s)
	}
}

func TestLLMJudgeNilClientSkips(t *testing.T) {
	if !(LLMJudge{}).Score(context.Background(), eval.Case{}, eval.RunRecord{}).Skipped {
		t.Fatal("nil client should skip")
	}
}

func TestLLMJudgeBadJSONErrors(t *testing.T) {
	j := LLMJudge{Client: judgeClient{out: "no json here"}}
	s := j.Score(context.Background(), eval.Case{}, eval.RunRecord{Result: &corereact.Result{}})
	if s.Err == nil {
		t.Fatal("bad json should error")
	}
}
