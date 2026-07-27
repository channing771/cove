package eval

import (
	"context"
	"testing"

	coreagent "github.com/boxify/api-go/internal/core/agent"
	"github.com/boxify/api-go/internal/core/agent/harness"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/llm"
)

func TestToolTrajectory(t *testing.T) {
	r := RunRecord{Result: &corereact.Result{
		Steps: []corereact.Step{
			{Action: "search"},
			{Action: ""}, // 终局步无 Action
			{Action: "calc"},
		},
		StoppedBy: coreagent.StopFinalAnswer,
	}}
	got := r.ToolTrajectory()
	if len(got) != 2 || got[0] != "search" || got[1] != "calc" {
		t.Fatalf("trajectory = %v", got)
	}
	if r.StopReason() != "final_answer" {
		t.Fatalf("stop = %q", r.StopReason())
	}
}

func TestToolTrajectoryNilSafe(t *testing.T) {
	var r RunRecord
	if r.ToolTrajectory() != nil || r.Answer() != "" || r.Iterations() != 0 || r.StopReason() != "" {
		t.Fatal("nil result accessors must be zero-valued")
	}
}

func TestWrapUsageCapturesToolCalling(t *testing.T) {
	base := &fakeToolClient{}
	base.usage = llm.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
	base.model = "m"
	cost := harness.CostTable{"m": {InputPer1K: 0.001, OutputPer1K: 0.002}}
	wc, u := wrapUsage(base, cost)
	tc, ok := wc.(llm.ToolCallingClient)
	if !ok {
		t.Fatal("wrapped tool client must expose ToolCallingClient")
	}
	if _, err := tc.InvokeWithTools(context.Background(), nil); err != nil {
		t.Fatalf("InvokeWithTools: %v", err)
	}
	if u.TotalTokens != 15 || u.InputTokens != 10 {
		t.Fatalf("usage = %+v", *u)
	}
	if u.CostUSD <= 0 {
		t.Fatalf("cost not accumulated: %v", u.CostUSD)
	}
}

func TestWrapUsageTextClientHidesToolCalling(t *testing.T) {
	base := &fakeTextClient{}
	wc, _ := wrapUsage(base, nil)
	if _, ok := wc.(llm.ToolCallingClient); ok {
		t.Fatal("text client must NOT expose ToolCallingClient")
	}
}
