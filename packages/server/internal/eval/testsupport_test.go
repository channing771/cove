package eval

import (
	"context"
	"errors"

	"github.com/boxify/api-go/internal/core/llm"
)

// fakeTextClient 只实现 llm.Client 核心方法(纯文本 ReAct 路径)。
type fakeTextClient struct {
	outputs []string
	usage   llm.TokenUsage
	model   string
}

func (f *fakeTextClient) Invoke(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (string, error) {
	if len(f.outputs) == 0 {
		return "", errors.New("no output")
	}
	out := f.outputs[0]
	f.outputs = f.outputs[1:]
	return out, nil
}
func (f *fakeTextClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	txt, err := f.Invoke(ctx, m, o...)
	if err != nil {
		return nil, err
	}
	return &llm.LLMResult{Text: txt, Usage: f.usage, Model: f.model}, nil
}
func (f *fakeTextClient) Stream(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (<-chan string, error) {
	return nil, errors.New("no stream")
}
func (f *fakeTextClient) Embed(ctx context.Context, texts []string, dim int, o ...llm.EmbeddingOption) ([][]float64, error) {
	return nil, errors.New("no embed")
}
func (f *fakeTextClient) EmbedOne(ctx context.Context, text string, dim int) ([]float64, error) {
	return nil, errors.New("no embed")
}

// fakeToolClient 额外实现 llm.ToolCallingClient(非流式工具调用路径,携带 usage)。
// usage/model 复用嵌入的 fakeTextClient 字段。
type fakeToolClient struct {
	fakeTextClient
	results []*llm.LLMResult
}

func (f *fakeToolClient) InvokeWithTools(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	if len(f.results) > 0 {
		r := f.results[0]
		f.results = f.results[1:]
		return r, nil
	}
	// 默认:无工具调用 = 终局答案,携带 usage。
	return &llm.LLMResult{Text: "final answer", Usage: f.usage, Model: f.model}, nil
}

func mustUsage(in, out int64) llm.TokenUsage {
	return llm.TokenUsage{InputTokens: in, OutputTokens: out, TotalTokens: in + out}
}
