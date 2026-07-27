package rag

import (
	"context"
	"time"
)

// RetrievedHit 是一条检索命中的评测视图:chunk 与所属文档的身份、内容与分数。
type RetrievedHit struct {
	ChunkID     string   `json:"chunk_id"`
	DocID       string   `json:"doc_id"`   // 文档/来源身份(ragsearch 的 Source.SourceID)
	DocName     string   `json:"doc_name"` // 文档名(Source.Name)
	Content     string   `json:"content"`
	Score       float64  `json:"score"`
	RerankScore *float64 `json:"rerank_score,omitempty"`
}

// identity 按匹配维度返回该命中的身份:match_on=="chunk" 用 chunk id,否则用文档 id。
func (h RetrievedHit) identity(matchOn string) string {
	if matchOn == "chunk" {
		return h.ChunkID
	}
	return h.DocID
}

// Retriever 是 RAG 检索的被测接口(SUT)。
//
// 生产实现由子包 ragadapter 包装 ragsearch.Searcher;单测用 fake。返回结果应按相关性
// 降序排列(rank 0 最相关)。
type Retriever interface {
	Retrieve(ctx context.Context, query string, topK int) ([]RetrievedHit, error)
}

// RetrievalRecord 表示一次检索的产物。
type RetrievalRecord struct {
	Hits        []RetrievedHit
	RequestedK  int
	Latency     time.Duration
	Err         error
}

// topKHits 返回前 k 条命中;k<=0 表示全部。
func (r RetrievalRecord) topKHits(k int) []RetrievedHit {
	if k > 0 && len(r.Hits) > k {
		return r.Hits[:k]
	}
	return r.Hits
}

// identities 返回前 k 条命中中去重(保序取首次)的身份列表。
func (r RetrievalRecord) identities(matchOn string, k int) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range r.topKHits(k) {
		id := h.identity(matchOn)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// relevanceFlags 返回前 k 条命中逐位是否命中 golden(用于 MRR / nDCG 的位置敏感计算)。
func (r RetrievalRecord) relevanceFlags(golden map[string]bool, matchOn string, k int) []bool {
	hits := r.topKHits(k)
	flags := make([]bool, len(hits))
	for i, h := range hits {
		flags[i] = golden[h.identity(matchOn)]
	}
	return flags
}
