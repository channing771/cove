package corpus

import (
	"context"
	"errors"
	"testing"

	corellm "github.com/boxify/api-go/internal/core/llm"
	ragsearch "github.com/boxify/api-go/internal/core/rag/search"
)

// recordingClient 记录 Embed 的入参,用于验证批量与维度透传。
type recordingClient struct {
	calls      [][]string
	lastDim    int
	lastOpts   corellm.EmbeddingOptions
	failOnCall int
	callN      int
}

func (c *recordingClient) Embed(ctx context.Context, texts []string, dimensions int, opts ...corellm.EmbeddingOption) ([][]float64, error) {
	c.callN++
	if c.failOnCall > 0 && c.callN == c.failOnCall {
		return nil, errors.New("boom")
	}
	c.calls = append(c.calls, append([]string(nil), texts...))
	c.lastDim = dimensions
	c.lastOpts = corellm.NewEmbeddingOptions(opts...)
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = []float64{float64(len(texts[i])), 1}
	}
	return out, nil
}

func (c *recordingClient) EmbedOne(ctx context.Context, text string, dimensions int) ([]float64, error) {
	v, err := c.Embed(ctx, []string{text}, dimensions)
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

func (c *recordingClient) Invoke(context.Context, []*corellm.Message, ...corellm.ModelCallOption) (string, error) {
	return "", errors.New("n/a")
}
func (c *recordingClient) InvokeResult(context.Context, []*corellm.Message, ...corellm.ModelCallOption) (*corellm.LLMResult, error) {
	return nil, errors.New("n/a")
}
func (c *recordingClient) Stream(context.Context, []*corellm.Message, ...corellm.ModelCallOption) (<-chan string, error) {
	return nil, errors.New("n/a")
}

func TestClientEmbedderPassesDimensionAndBatch(t *testing.T) {
	rc := &recordingClient{}
	e := NewClientEmbedder(rc, 1024, 2)
	got, err := e.Embed(context.Background(), []string{"a", "bb", "ccc"}, 0)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("vectors = %d, want 3", len(got))
	}
	if rc.lastDim != 1024 {
		t.Fatalf("dimensions = %d, want 1024(构造维度应在 dimensions<=0 时生效)", rc.lastDim)
	}
	if rc.lastOpts.BatchSize != 2 {
		t.Fatalf("batch size = %d, want 2", rc.lastOpts.BatchSize)
	}
}

// dimensions>0 时以调用方传入为准(检索侧按 collection 维度调用)。
func TestClientEmbedderExplicitDimensionWins(t *testing.T) {
	rc := &recordingClient{}
	e := NewClientEmbedder(rc, 1024, 0)
	if _, err := e.EmbedOne(context.Background(), "q", 512); err != nil {
		t.Fatalf("EmbedOne: %v", err)
	}
	if rc.lastDim != 512 {
		t.Fatalf("dimensions = %d, want 512", rc.lastDim)
	}
}

func TestClientEmbedderPropagatesError(t *testing.T) {
	e := NewClientEmbedder(&recordingClient{failOnCall: 1}, 8, 0)
	if _, err := e.EmbedOne(context.Background(), "q", 0); err == nil {
		t.Fatal("底层错误应向上传播")
	}
}

func TestClientEmbedderNilClient(t *testing.T) {
	if _, err := NewClientEmbedder(nil, 8, 0).EmbedOne(context.Background(), "q", 0); err == nil {
		t.Fatal("nil client 应报错而非 panic")
	}
}

// 同时满足摄入侧 BatchEmbedder 与检索侧 ragsearch.Embedder。
var (
	_ BatchEmbedder       = (*ClientEmbedder)(nil)
	_ ragsearch.Embedder  = (*ClientEmbedder)(nil)
)
