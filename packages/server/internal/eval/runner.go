package eval

import (
	"context"
	"errors"
	"time"

	"github.com/boxify/api-go/internal/core/agent/harness"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/llm"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

// Runner 是被评估对象(SUT)的适配器:把一条用例跑成一条运行记录。
type Runner interface {
	Run(ctx context.Context, c Case) (RunRecord, error)
}

// HarnessRunner 用 harness 包裹的 agent 作为 SUT。
//
// Options 为静态 harness 选项(budget/policy/prompt/maxIterations 等),不得包含
// WithCostModel/WithDeterminism——运行器内部注入 usage 捕获与(按 Case.Cassette)回放。
type HarnessRunner struct {
	Client   llm.Client
	Registry *coretool.Registry
	Cost     harness.CostModel
	Options  []harness.Option
}

// Run 跑一条用例:注入 usage 捕获与可选回放,计时执行,组装 RunRecord。
// agent 运行错误落入 RunRecord.Err;返回的 error 仅用于基础设施级失败。
func (r *HarnessRunner) Run(ctx context.Context, c Case) (RunRecord, error) {
	if r.Client == nil {
		return RunRecord{}, errors.New("eval: HarnessRunner.Client is nil")
	}
	client, usage := wrapUsage(r.Client, r.Cost)

	opts := make([]harness.Option, 0, len(r.Options)+2)
	opts = append(opts, r.Options...)
	opts = append(opts, harness.WithCostModel(r.Cost))
	if c.Cassette != "" {
		opts = append(opts, harness.WithDeterminism(harness.DeterminismReplay, c.Cassette))
	}

	h := harness.New(client, r.Registry, opts...)
	start := time.Now()
	res, runErr := h.Run(ctx, corereact.Input{Query: c.Query, Messages: c.Messages})
	latency := time.Since(start)

	return RunRecord{Result: res, Latency: latency, Usage: *usage, Err: runErr}, nil
}

var _ Runner = (*HarnessRunner)(nil)
