package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	coreagent "github.com/boxify/api-go/internal/core/agent"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

// TestHarness_RunRetriesTextReActPath 验证文本 ReAct 路径（Invoke）经 harness 重试后成功。
func TestHarness_RunRetriesTextReActPath(t *testing.T) {
	var calls int
	base := &scriptedClient{invoke: func(context.Context) (string, error) {
		calls++
		if calls < 2 {
			return "", errors.New("flaky")
		}
		return "Final Answer: done", nil
	}}
	h := New(base, coretool.NewRegistry(),
		WithRetry(RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, Retryable: func(error) bool { return true }}),
	)
	res, err := h.Run(context.Background(), corereact.Input{Query: "hi"})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.StoppedBy != corereact.StopFinalAnswer {
		t.Fatalf("stopped=%v answer=%q", res.StoppedBy, res.Answer)
	}
	if calls != 2 {
		t.Fatalf("应重试一次后成功, calls=%d", calls)
	}
}

// TestHarness_ToolCallBudgetStops 验证工具调用预算越界经治理 hook 映射为 StopBudgetExceeded。
func TestHarness_ToolCallBudgetStops(t *testing.T) {
	base := &scriptedClient{invoke: func(context.Context) (string, error) {
		return "Action: echo\nAction Input: {}", nil
	}}
	reg := coretool.NewRegistry()
	if err := reg.Register(context.Background(), coretool.NewFuncTool(
		coretool.Descriptor{Name: "echo"},
		func(context.Context, coretool.Input) (coretool.Output, error) {
			return coretool.Output{Text: "ok"}, nil
		},
	)); err != nil {
		t.Fatal(err)
	}
	h := New(base, reg, WithBudgetConfig(BudgetConfig{MaxToolCalls: 1}), WithMaxIterations(5))
	res, _ := h.Run(context.Background(), corereact.Input{Query: "hi"})
	if res.StoppedBy != coreagent.StopBudgetExceeded {
		t.Fatalf("工具调用越界应停在 StopBudgetExceeded, got %v", res.StoppedBy)
	}
}

// TestHarness_PolicyFiltersTools 验证白名单外的工具不会暴露给模型。
func TestHarness_PolicyFiltersTools(t *testing.T) {
	reg := coretool.NewRegistry()
	for _, name := range []string{"allowed", "blocked"} {
		if err := reg.Register(context.Background(), coretool.NewFuncTool(
			coretool.Descriptor{Name: name},
			func(context.Context, coretool.Input) (coretool.Output, error) { return coretool.Output{}, nil },
		)); err != nil {
			t.Fatal(err)
		}
	}
	h := New(&scriptedClient{}, reg, WithPolicy(Policy{Allowlist: []string{"allowed"}}))
	built, err := h.buildRegistry(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := built.Lookup("blocked"); ok {
		t.Fatal("白名单外工具不应出现在装配后的 registry")
	}
	if _, ok := built.Lookup("allowed"); !ok {
		t.Fatal("白名单内工具应保留")
	}
}
