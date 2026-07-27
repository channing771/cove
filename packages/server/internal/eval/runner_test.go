package eval

import (
	"context"
	"testing"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
)

func TestHarnessRunnerToolCallingCapturesUsage(t *testing.T) {
	client := &fakeToolClient{}
	client.usage = mustUsage(12, 8)
	client.model = "m"
	r := &HarnessRunner{Client: client}
	rec, err := r.Run(context.Background(), Case{ID: "c1", Query: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Answer() != "final answer" {
		t.Fatalf("answer = %q", rec.Answer())
	}
	if rec.StopReason() != string(corereact.StopFinalAnswer) {
		t.Fatalf("stop = %q", rec.StopReason())
	}
	if rec.Iterations() != 1 {
		t.Fatalf("iters = %d", rec.Iterations())
	}
	if rec.Latency <= 0 {
		t.Fatal("latency must be > 0")
	}
	if rec.Usage.TotalTokens != 20 {
		t.Fatalf("total tokens = %d, want 20", rec.Usage.TotalTokens)
	}
}
