package harness

import (
	"context"

	"github.com/boxify/api-go/internal/core/llm"
)

// clientMiddleware 装饰 llm.Client 的非流式生成路径（Invoke / InvokeResult）。
//
// 中间件只包裹非流式文本与结构化生成；流式（StreamEvents / StreamWithTools）与
// 原生工具调用（InvokeWithTools）、Vision 由 forwardOptional 直接转发到底层 base，
// 不经过中间件。原因：生产聊天走流式工具路径，其可靠性/预算护栏由 harness 的
// Hooks 层（工具调用计数、墙钟、可观测）承担；client 装饰器聚焦非流式消费方。
type clientMiddleware func(llm.Client) llm.Client

// chainClient 依次套用中间件（后者在外层），并保留 base 的可选接口能力。
//
// 返回值对 llm.ToolCallingClient / llm.StreamEventClient / llm.ToolStreamEventClient /
// llm.VisionClient 的实现情况与 base 完全一致，确保 react.AutoPlanner 的能力探测不被扭曲。
func chainClient(base llm.Client, mws ...clientMiddleware) llm.Client {
	wrapped := base
	for _, mw := range mws {
		if mw != nil {
			wrapped = mw(wrapped)
		}
	}
	return forwardOptional(base, wrapped)
}

// forwardOptional 让装饰后的 wrapped 暴露与 base 相同的可选接口。
//
// core Client 方法走 wrapped（含中间件）；可选接口方法委托 base 原始实现。
// 只枚举真实出现的能力组合：无 / 仅工具调用（测试）/ 工具+流式（anthropic）/
// 工具+流式+Vision（openai）。未覆盖的组合退化为仅暴露 core Client（安全但不暴露可选接口）。
func forwardOptional(base, wrapped llm.Client) llm.Client {
	if base == wrapped {
		return base
	}
	_, hasTC := base.(llm.ToolCallingClient)
	_, hasSE := base.(llm.StreamEventClient)
	_, hasTSE := base.(llm.ToolStreamEventClient)
	_, hasVC := base.(llm.VisionClient)

	switch {
	case hasTC && hasSE && hasTSE && hasVC:
		return tcStreamVisionShim{Client: wrapped, base: base}
	case hasTC && hasSE && hasTSE:
		return tcStreamShim{Client: wrapped, base: base}
	case hasTC:
		return tcShim{Client: wrapped, base: base}
	default:
		return wrapped
	}
}

type tcShim struct {
	llm.Client
	base llm.Client
}

func (s tcShim) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return s.base.(llm.ToolCallingClient).InvokeWithTools(ctx, m, o...)
}

type tcStreamShim struct {
	llm.Client
	base llm.Client
}

func (s tcStreamShim) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return s.base.(llm.ToolCallingClient).InvokeWithTools(ctx, m, o...)
}

func (s tcStreamShim) StreamEvents(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan llm.StreamEvent, error) {
	return s.base.(llm.StreamEventClient).StreamEvents(ctx, m, o...)
}

func (s tcStreamShim) StreamWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan llm.StreamEvent, error) {
	return s.base.(llm.ToolStreamEventClient).StreamWithTools(ctx, m, o...)
}

type tcStreamVisionShim struct {
	llm.Client
	base llm.Client
}

func (s tcStreamVisionShim) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	return s.base.(llm.ToolCallingClient).InvokeWithTools(ctx, m, o...)
}

func (s tcStreamVisionShim) StreamEvents(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan llm.StreamEvent, error) {
	return s.base.(llm.StreamEventClient).StreamEvents(ctx, m, o...)
}

func (s tcStreamVisionShim) StreamWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan llm.StreamEvent, error) {
	return s.base.(llm.ToolStreamEventClient).StreamWithTools(ctx, m, o...)
}

func (s tcStreamVisionShim) Vision(ctx context.Context, prompt, imageBase64, mime string, o ...llm.ModelCallOption) (*llm.VisionResult, error) {
	return s.base.(llm.VisionClient).Vision(ctx, prompt, imageBase64, mime, o...)
}
