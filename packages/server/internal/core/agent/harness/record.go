package harness

import (
	"context"
	"errors"

	"github.com/boxify/api-go/internal/core/llm"
)

// DeterminismMode 控制确定性录制/回放行为。
type DeterminismMode int

const (
	// DeterminismOff 不录制也不回放（默认）。
	DeterminismOff DeterminismMode = iota
	// DeterminismRecord 调用真实底层并把结果录入磁带。
	DeterminismRecord
	// DeterminismReplay 从磁带回放，不触达底层。
	DeterminismReplay
)

// ErrReplayMiss 表示回放时磁带未命中且为严格模式。
var ErrReplayMiss = errors.New("harness: replay cassette miss")

// withRecord 返回把每次结构化生成结果录入磁带的中间件。
func withRecord(c *Cassette) clientMiddleware {
	if c == nil {
		return func(cl llm.Client) llm.Client { return cl }
	}
	return func(cl llm.Client) llm.Client { return &recordClient{Client: cl, cassette: c} }
}

type recordClient struct {
	llm.Client
	cassette *Cassette
}

func (r *recordClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	res, err := r.Client.InvokeResult(ctx, m, o...)
	if err != nil {
		return res, err
	}
	r.cassette.Append(fingerprint(m), res)
	return res, nil
}

// withReplay 返回从磁带回放结构化生成的中间件。strict 为 true 时未命中返回 ErrReplayMiss，
// 否则透传底层。
func withReplay(c *Cassette, strict bool) clientMiddleware {
	if c == nil {
		return func(cl llm.Client) llm.Client { return cl }
	}
	return func(cl llm.Client) llm.Client { return &replayClient{Client: cl, cassette: c, strict: strict} }
}

type replayClient struct {
	llm.Client
	cassette *Cassette
	strict   bool
}

func (r *replayClient) InvokeResult(ctx context.Context, m []*llm.Message, o ...llm.ModelCallOption) (*llm.LLMResult, error) {
	if res, ok := r.cassette.Find(fingerprint(m)); ok {
		return res, nil
	}
	if r.strict {
		return nil, ErrReplayMiss
	}
	return r.Client.InvokeResult(ctx, m, o...)
}
