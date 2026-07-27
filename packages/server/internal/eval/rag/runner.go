package rag

import (
	"context"
	"errors"
	"time"

	"github.com/boxify/api-go/internal/eval"
)

// RetrievalRunner 用一个 Retriever 把用例跑成 RetrievalRecord。
//
// TopK<=0 时默认 5;用例 Expect["k"] 若存在则覆盖本次检索的 top_k。
type RetrievalRunner struct {
	Retriever Retriever
	TopK      int
}

// Run 对用例执行检索并计时。检索错误落入 RetrievalRecord.Err(不作为返回 error),
// 使 scorer 仍可据空结果打分;仅基础设施级失败(Retriever 为 nil)返回 error。
func (r *RetrievalRunner) Run(ctx context.Context, c eval.Case) (RetrievalRecord, error) {
	if r.Retriever == nil {
		return RetrievalRecord{}, errors.New("rag: RetrievalRunner.Retriever is nil")
	}
	k := r.TopK
	if k <= 0 {
		k = 5
	}
	if v, ok := eval.ExpectInt(c, "k"); ok && v > 0 {
		k = v
	}
	// 检索器若实现 RelevanceAwareRetriever,顺带取回生产的低相关判定供负例打分。
	start := time.Now()
	var (
		hits      []RetrievedHit
		relevance Relevance
		err       error
	)
	if aware, ok := r.Retriever.(RelevanceAwareRetriever); ok {
		hits, relevance, err = aware.RetrieveWithRelevance(ctx, c.Query, k)
	} else {
		hits, err = r.Retriever.Retrieve(ctx, c.Query, k)
	}
	latency := time.Since(start)
	return RetrievalRecord{Hits: hits, RequestedK: k, Latency: latency, Relevance: relevance, Err: err}, nil
}
