package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/boxify/api-go/internal/core/llm"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

func TestBudget_TokensExceeded(t *testing.T) {
	b := NewBudget(BudgetConfig{MaxTotalTokens: 100})
	if err := b.AddUsage(llm.TokenUsage{TotalTokens: 60}, 0); err != nil {
		t.Fatalf("首次不应越界: %v", err)
	}
	if err := b.AddUsage(llm.TokenUsage{TotalTokens: 60}, 0); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("累计越界应返回 ErrBudgetExceeded, got %v", err)
	}
}

func TestBudget_ToolCallsExceeded(t *testing.T) {
	b := NewBudget(BudgetConfig{MaxToolCalls: 1})
	if err := b.AddToolCall(); err != nil {
		t.Fatal(err)
	}
	if err := b.AddToolCall(); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatal("第2次工具调用应越界")
	}
}

func TestCostTable_Cost(t *testing.T) {
	table := CostTable{"gpt-x": {InputPer1K: 0.01, OutputPer1K: 0.03}}
	got := table.Cost("gpt-x", llm.TokenUsage{InputTokens: 1000, OutputTokens: 1000})
	if got != 0.04 {
		t.Fatalf("want 0.04, got %v", got)
	}
	if table.Cost("unknown", llm.TokenUsage{InputTokens: 1000}) != 0 {
		t.Fatal("未知模型应返回 0")
	}
}

func TestBudgetClient_StopsWhenExceeded(t *testing.T) {
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) {
		return &llm.LLMResult{Text: "x", Usage: llm.TokenUsage{TotalTokens: 200}}, nil
	}}
	b := NewBudget(BudgetConfig{MaxTotalTokens: 100})
	c := chainClient(base, withBudget(b, CostTable{}))
	if _, err := c.InvokeResult(context.Background(), nil); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("want ErrBudgetExceeded, got %v", err)
	}
}

func TestGuardTool_RefuseByDefault(t *testing.T) {
	base := coretool.NewFuncTool(coretool.Descriptor{Name: "danger"}, func(context.Context, coretool.Input) (coretool.Output, error) {
		return coretool.Output{Text: "did danger"}, nil
	})
	g := guardTool(base, Policy{Authorize: func(context.Context, string, coretool.Input) bool { return false }})
	out, err := g.Invoke(context.Background(), coretool.Input{})
	if err != nil {
		t.Fatalf("默认拒绝应返回观察结果而非错误: %v", err)
	}
	if out.Text == "did danger" {
		t.Fatal("未授权工具不应真正执行")
	}
}

func TestGuardTool_HardStop(t *testing.T) {
	base := coretool.NewFuncTool(coretool.Descriptor{Name: "danger"}, func(context.Context, coretool.Input) (coretool.Output, error) {
		return coretool.Output{}, nil
	})
	g := guardTool(base, Policy{Mode: PolicyHardStop, Authorize: func(context.Context, string, coretool.Input) bool { return false }})
	if _, err := g.Invoke(context.Background(), coretool.Input{}); !errors.Is(err, ErrToolDenied) {
		t.Fatalf("硬停模式应返回 ErrToolDenied, got %v", err)
	}
}

func TestGuardTool_AllowsAuthorized(t *testing.T) {
	base := coretool.NewFuncTool(coretool.Descriptor{Name: "ok"}, func(context.Context, coretool.Input) (coretool.Output, error) {
		return coretool.Output{Text: "done"}, nil
	})
	g := guardTool(base, Policy{Authorize: func(context.Context, string, coretool.Input) bool { return true }})
	out, err := g.Invoke(context.Background(), coretool.Input{})
	if err != nil || out.Text != "done" {
		t.Fatalf("授权工具应正常执行, err=%v out=%v", err, out)
	}
}

func TestPolicy_Allowlist(t *testing.T) {
	p := Policy{Allowlist: []string{"a", "b"}}
	if !p.allowed("a") || p.allowed("c") {
		t.Fatal("白名单判定错误")
	}
	if !(Policy{}).allowed("anything") {
		t.Fatal("空白名单应放行全部")
	}
}
