package scorers

import (
	"context"
	"testing"
	"time"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/eval"
)

func TestLatencyBudget(t *testing.T) {
	r := eval.RunRecord{Result: &corereact.Result{}, Latency: 500 * time.Millisecond}
	ok := LatencyBudget().Score(context.Background(), eval.Case{Expect: map[string]any{"latency_ms": float64(1000)}}, r)
	if !ok.Passed {
		t.Fatalf("500ms should pass 1000ms budget: %+v", ok)
	}
	bad := LatencyBudget().Score(context.Background(), eval.Case{Expect: map[string]any{"latency_ms": float64(100)}}, r)
	if bad.Passed {
		t.Fatal("500ms should fail 100ms budget")
	}
}

func TestTokenBudget(t *testing.T) {
	r := eval.RunRecord{Result: &corereact.Result{}, Usage: eval.Usage{TotalTokens: 120}}
	if TokenBudget().Score(context.Background(), eval.Case{Expect: map[string]any{"token_budget": float64(100)}}, r).Passed {
		t.Fatal("120 > 100 should fail")
	}
	// 无 usage 数据(流式路径) → 跳过
	r0 := eval.RunRecord{Result: &corereact.Result{}, Usage: eval.Usage{TotalTokens: 0}}
	if !TokenBudget().Score(context.Background(), eval.Case{Expect: map[string]any{"token_budget": float64(100)}}, r0).Skipped {
		t.Fatal("zero usage should skip token budget")
	}
}
