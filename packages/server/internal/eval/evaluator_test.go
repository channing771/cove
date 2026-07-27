package eval

import (
	"context"
	"testing"

	coreagent "github.com/boxify/api-go/internal/core/agent"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
)

type stubRunner struct{ answer string }

func (s stubRunner) Run(ctx context.Context, c Case) (RunRecord, error) {
	return RunRecord{Result: &corereact.Result{Answer: s.answer, Iterations: 1, StoppedBy: coreagent.StopFinalAnswer}}, nil
}

type containsScorer struct{}

func (containsScorer) Name() string { return "contains" }
func (containsScorer) Score(_ context.Context, c Case, r RunRecord) Score {
	want, ok := ExpectString(c, "want")
	if !ok {
		return Score{Scorer: "contains", Skipped: true, Passed: true}
	}
	if r.Answer() == want {
		return Score{Scorer: "contains", Passed: true, Value: 1}
	}
	return Score{Scorer: "contains", Passed: false}
}

func TestEvaluatorAggregates(t *testing.T) {
	ds := &Dataset{Name: "t", Cases: []Case{
		{ID: "a", Expect: map[string]any{"want": "ok"}},
		{ID: "b", Expect: map[string]any{"want": "nope"}},
	}}
	e := &Evaluator{Runner: stubRunner{answer: "ok"}, Scorers: []Scorer{containsScorer{}}}
	rep, err := e.Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Cases) != 2 {
		t.Fatalf("cases = %d", len(rep.Cases))
	}
	if rep.PassRate != 0.5 {
		t.Fatalf("pass rate = %v, want 0.5", rep.PassRate)
	}
	agg := rep.Scorers["contains"]
	if agg.Passed != 1 || agg.Failed != 1 {
		t.Fatalf("agg = %+v", agg)
	}
}
