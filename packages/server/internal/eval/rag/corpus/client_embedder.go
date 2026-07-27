package corpus

import (
	"context"
	"errors"

	corellm "github.com/boxify/api-go/internal/core/llm"
	infrallm "github.com/boxify/api-go/internal/infrastructure/llm"
)

// GLMBaseURL 是智谱 GLM 的 OpenAI 兼容 API 端点。
//
// GLM 的 /embeddings 与 OpenAI 同构,故直接复用仓库既有的 OpenAI 兼容客户端,
// 无需新增 provider(zhipu 本就走 OpenAICompatibleFactory)。
const GLMBaseURL = "https://open.bigmodel.cn/api/paas/v4"

// GLMEmbeddingModel 是 GLM 的向量模型 embedding-3。
//
// 支持 256/512/1024/2048 维;本仓库 RagConfig.EmbeddingDim 默认 1024。
const GLMEmbeddingModel = "embedding-3"

// GLMEmbeddingBatchSize 是单次请求的文本条数上限(GLM 对 embeddings 输入数组有限制,
// 取保守值以免长语料摄入被拒)。
const GLMEmbeddingBatchSize = 32

// embeddingClient 是 ClientEmbedder 依赖的最小能力集(llm.Client 的向量部分)。
type embeddingClient interface {
	Embed(ctx context.Context, texts []string, dimensions int, opts ...corellm.EmbeddingOption) ([][]float64, error)
	EmbedOne(ctx context.Context, text string, dimensions int) ([]float64, error)
}

// ClientEmbedder 把任意 llm.Client 适配为评测所需的嵌入器。
//
// 同时满足摄入侧 BatchEmbedder 与检索侧 ragsearch.Embedder:前者接口不带可变参数,
// 后者只需 EmbedOne,故需要这层适配。Dim 在调用方未指定维度时生效;BatchSize>0 时
// 按批切分请求(真实向量服务对单请求输入条数有上限)。
type ClientEmbedder struct {
	client    embeddingClient
	Dim       int
	BatchSize int
}

// NewClientEmbedder 用给定模型客户端构造嵌入器。dim<=0 时沿用调用方传入的维度。
func NewClientEmbedder(client embeddingClient, dim, batchSize int) *ClientEmbedder {
	return &ClientEmbedder{client: client, Dim: dim, BatchSize: batchSize}
}

// NewGLMEmbedder 构造使用 GLM embedding-3 的嵌入器。
//
// model 为空时用 GLMEmbeddingModel;dim<=0 时用 1024;baseURL 为空时用 GLMBaseURL
// (便于指向兼容网关)。返回的嵌入器可直接用于 Ingester 与 SearcherRetriever。
func NewGLMEmbedder(apiKey, model, baseURL string, dim int) (*ClientEmbedder, error) {
	if apiKey == "" {
		return nil, errors.New("corpus: GLM API key 为空")
	}
	if model == "" {
		model = GLMEmbeddingModel
	}
	if baseURL == "" {
		baseURL = GLMBaseURL
	}
	if dim <= 0 {
		dim = 1024
	}
	client, err := infrallm.NewOpenAICompatibleFactory().NewClient(corellm.ModelConfig{
		Provider:       "zhipu",
		Model:          model,
		APIKey:         apiKey,
		BaseURL:        baseURL,
		EmbeddingModel: model,
	})
	if err != nil {
		return nil, err
	}
	return NewClientEmbedder(client, dim, GLMEmbeddingBatchSize), nil
}

// resolveDim 优先使用调用方显式维度,否则用构造维度。
func (e *ClientEmbedder) resolveDim(dimensions int) int {
	if dimensions > 0 {
		return dimensions
	}
	return e.Dim
}

func (e *ClientEmbedder) opts() []corellm.EmbeddingOption {
	if e.BatchSize > 0 {
		return []corellm.EmbeddingOption{corellm.WithEmbeddingBatchSize(e.BatchSize)}
	}
	return nil
}

// Embed 批量向量化(摄入侧)。
func (e *ClientEmbedder) Embed(ctx context.Context, texts []string, dimensions int) ([][]float64, error) {
	if e == nil || e.client == nil {
		return nil, errors.New("corpus: 嵌入客户端为空")
	}
	return e.client.Embed(ctx, texts, e.resolveDim(dimensions), e.opts()...)
}

// EmbedOne 单条向量化(检索侧,满足 ragsearch.Embedder)。
func (e *ClientEmbedder) EmbedOne(ctx context.Context, text string, dimensions int) ([]float64, error) {
	if e == nil || e.client == nil {
		return nil, errors.New("corpus: 嵌入客户端为空")
	}
	return e.client.EmbedOne(ctx, text, e.resolveDim(dimensions))
}
