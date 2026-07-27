//go:build eval

package eval_test

import (
	"context"
	"testing"

	"github.com/boxify/api-go/internal/core/llm"
	"github.com/boxify/api-go/internal/eval"
	"github.com/boxify/api-go/internal/eval/scorers"
)

// gateClient 是实现 ToolCallingClient 的 hermetic fake:无工具调用即终局答案,携带 usage。
type gateClient struct{}

func (gateClient) Invoke(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (string, error) {
	return "Thought: done\nFinal Answer: 42", nil
}
func (gateClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return &llm.LLMResult{Text: "42", Usage: llm.TokenUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}, Model: "gate"}, nil
}
func (gateClient) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return &llm.LLMResult{Text: "42", Usage: llm.TokenUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}, Model: "gate"}, nil
}
func (gateClient) Stream(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan string, error) {
	return nil, nil
}
func (gateClient) Embed(ctx context.Context, t []string, d int, o ...llm.EmbeddingOption) ([][]float64, error) {
	return nil, nil
}
func (gateClient) EmbedOne(ctx context.Context, t string, d int) ([]float64, error) { return nil, nil }

func TestEvalGate(t *testing.T) {
	ds, err := eval.LoadDataset("testdata/datasets/gate.json")
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	runner := &eval.HarnessRunner{Client: gateClient{}}
	e := &eval.Evaluator{
		Runner: runner,
		Scorers: []eval.Scorer{
			scorers.Contains(),
			scorers.StopReasonIs(),
			scorers.MaxIterations(),
			scorers.LatencyBudget(),
			scorers.TokenBudget(),
		},
	}
	rep, err := e.Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("eval run: %v", err)
	}
	_ = rep.WriteTable(testWriter{t})
	baseline, err := eval.LoadReport("testdata/baselines/gate.json")
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	eval.AssertNoRegression(t, rep, baseline)
	if rep.PassRate != 1 {
		t.Fatalf("pass rate = %v, want 1", rep.PassRate)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }
