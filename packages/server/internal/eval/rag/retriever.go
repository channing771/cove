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

// identity 按匹配维度返回该命中的身份。
//
// match_on 取值:"chunk" 用 chunk id;"name" 用文档名(便于数据集用可读文件名做 golden
// 标注,而非 UUID);其余(默认 "doc")用文档来源 id。
func (h RetrievedHit) identity(matchOn string) string {
	switch matchOn {
	case "chunk":
		return h.ChunkID
	case "name":
		return h.DocName
	default:
		return h.DocID
	}
}

// Relevance 是生产检索的整体低相关判定(ragsearch.RelevanceStatus 的评测视图)。
//
// Low 表示本次检索最高分低于阈值——业务层据此决定是否使用这些结果。评测用它覆盖负例:
// 知识库里没有答案的问题,检索器仍会返回"最像"的若干块,但必须被标记为低相关。
type Relevance struct {
	Low       bool     `json:"low"`
	Basis     string   `json:"basis,omitempty"` // vector | rerank | 空
	MaxScore  *float64 `json:"max_score,omitempty"`
	Threshold *float64 `json:"threshold,omitempty"`
}

// Retriever 是 RAG 检索的被测接口(SUT)。
//
// 生产实现由子包 ragadapter 包装 ragsearch.Searcher;单测用 fake。返回结果应按相关性
// 降序排列(rank 0 最相关)。
type Retriever interface {
	Retrieve(ctx context.Context, query string, topK int) ([]RetrievedHit, error)
}

// RelevanceAwareRetriever 是可选接口:除命中外额外报告低相关判定。
//
// RetrievalRunner 会自动探测该接口;未实现时 RetrievalRecord.Relevance 为零值,
// 相关打分器(LowRelevanceIs)对该用例的判定将失败而非静默通过——故负例数据集应搭配
// 实现了本接口的检索器(如 ragadapter.SearcherRetriever)。
type RelevanceAwareRetriever interface {
	Retriever
	RetrieveWithRelevance(ctx context.Context, query string, topK int) ([]RetrievedHit, Relevance, error)
}

// RetrievalRecord 表示一次检索的产物。
type RetrievalRecord struct {
	Hits       []RetrievedHit
	RequestedK int
	Latency    time.Duration
	Relevance  Relevance
	Err        error
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

// relevanceFlags 返回前 k 条命中按身份去重后、逐位是否命中 golden。
//
// 必须去重:doc 级匹配下同一文档常有多个 chunk 命中,若逐 chunk 计入,DCG 会把同一篇
// 文档重复累加,而 IDCG 以 golden 文档数封顶,导致 nDCG 溢出 (0,1] 区间。去重后与
// Recall/Precision 的口径一致(均以"检索到的不同文档"为单位)。
func (r RetrievalRecord) relevanceFlags(golden map[string]bool, matchOn string, k int) []bool {
	seen := map[string]bool{}
	var flags []bool
	for _, h := range r.topKHits(k) {
		id := h.identity(matchOn)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		flags = append(flags, golden[id])
	}
	return flags
}
