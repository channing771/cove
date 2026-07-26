package harness

import (
	"context"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/core/llm"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

// MultiHooks 把多个 hooks 组合为一个，按序调用同名方法，首个非 nil error 短路返回。
// nil 成员会被跳过。
func MultiHooks(hooks ...corereact.Hooks) corereact.Hooks {
	out := make([]corereact.Hooks, 0, len(hooks))
	for _, h := range hooks {
		if h != nil {
			out = append(out, h)
		}
	}
	return multiHooks(out)
}

type multiHooks []corereact.Hooks

func (m multiHooks) BeforeRun(ctx context.Context, s corereact.State) error {
	for _, h := range m {
		if err := h.BeforeRun(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHooks) AfterRun(ctx context.Context, result corereact.Result, runErr error) error {
	for _, h := range m {
		if err := h.AfterRun(ctx, result, runErr); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHooks) BeforeTransition(ctx context.Context, s corereact.State, tr corereact.Transition) error {
	for _, h := range m {
		if err := h.BeforeTransition(ctx, s, tr); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHooks) AfterTransition(ctx context.Context, s corereact.State, tr corereact.Transition) error {
	for _, h := range m {
		if err := h.AfterTransition(ctx, s, tr); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHooks) BeforeModel(ctx context.Context, s corereact.State, msgs []*llm.Message) error {
	for _, h := range m {
		if err := h.BeforeModel(ctx, s, msgs); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHooks) OnToken(ctx context.Context, s corereact.State, text string) error {
	for _, h := range m {
		if err := h.OnToken(ctx, s, text); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHooks) AfterModel(ctx context.Context, s corereact.State, output string, modelErr error) error {
	for _, h := range m {
		if err := h.AfterModel(ctx, s, output, modelErr); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHooks) AfterParse(ctx context.Context, s corereact.State, d corereact.Decision, parseErr error) error {
	for _, h := range m {
		if err := h.AfterParse(ctx, s, d, parseErr); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHooks) BeforeTool(ctx context.Context, s corereact.State, call corereact.ToolCall) error {
	for _, h := range m {
		if err := h.BeforeTool(ctx, s, call); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHooks) AfterTool(ctx context.Context, s corereact.State, call corereact.ToolCall, out coretool.Output, toolErr error) error {
	for _, h := range m {
		if err := h.AfterTool(ctx, s, call, out, toolErr); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHooks) OnStep(ctx context.Context, s corereact.State, step corereact.Step) error {
	for _, h := range m {
		if err := h.OnStep(ctx, s, step); err != nil {
			return err
		}
	}
	return nil
}

func (m multiHooks) OnError(ctx context.Context, s corereact.State, err error) error {
	for _, h := range m {
		if e := h.OnError(ctx, s, err); e != nil {
			return e
		}
	}
	return nil
}
