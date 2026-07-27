package scorers

import (
	"context"
	"testing"

	coreagent "github.com/boxify/api-go/internal/core/agent"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/eval"
)

func rec(answer string, steps ...string) eval.RunRecord {
	rr := &corereact.Result{Answer: answer, Iterations: len(steps) + 1, StoppedBy: coreagent.StopFinalAnswer}
	for _, s := range steps {
		rr.Steps = append(rr.Steps, corereact.Step{Action: s})
	}
	return eval.RunRecord{Result: rr}
}

func TestContains(t *testing.T) {
	s := Contains()
	got := s.Score(context.Background(), eval.Case{Expect: map[string]any{"contains": []any{"向量"}}}, rec("向量数据库存的是向量"))
	if !got.Passed {
		t.Fatalf("want pass, got %+v", got)
	}
	miss := s.Score(context.Background(), eval.Case{Expect: map[string]any{"contains": "缺失"}}, rec("无关内容"))
	if miss.Passed {
		t.Fatal("want fail")
	}
	sk := s.Score(context.Background(), eval.Case{Expect: map[string]any{}}, rec("x"))
	if !sk.Skipped {
		t.Fatal("want skipped when no expectation")
	}
}

func TestToolTrajectoryContainsAndExact(t *testing.T) {
	s := ToolTrajectory()
	c := eval.Case{Expect: map[string]any{"tools": map[string]any{"mode": "exact", "names": []any{"search", "calc"}}}}
	if !s.Score(context.Background(), c, rec("a", "search", "calc")).Passed {
		t.Fatal("exact should pass")
	}
	if s.Score(context.Background(), c, rec("a", "search")).Passed {
		t.Fatal("exact mismatch should fail")
	}
}

func TestStopReasonAndMaxIterations(t *testing.T) {
	if !StopReasonIs().Score(context.Background(), eval.Case{Expect: map[string]any{"stop_reason": "final_answer"}}, rec("x")).Passed {
		t.Fatal("stop reason should pass")
	}
	if MaxIterations().Score(context.Background(), eval.Case{Expect: map[string]any{"max_iterations": float64(1)}}, rec("x", "t1")).Passed {
		t.Fatal("2 iters > max 1 should fail")
	}
}
