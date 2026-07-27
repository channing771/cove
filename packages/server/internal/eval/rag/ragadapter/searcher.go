// Package ragadapter 把生产的 ragsearch.Searcher 桥接为 rag.Retriever。
//
// 单列子包,使核心 rag 评测包不必依赖 ragsearch/vectorstore/models —— 单测用 fake 即可
// hermetic 运行;本包仅在对真实(或以 fake 端口驱动的)检索器做集成评测时引入。
package ragadapter

import (
	"context"

	ragsearch "github.com/boxify/api-go/internal/core/rag/search"
	"github.com/boxify/api-go/internal/core/rag/vectorstore"
	"github.com/boxify/api-go/internal/eval/rag"
	"github.com/boxify/api-go/internal/models"
)

// SearcherRetriever 用生产 Searcher 实现 rag.Retriever。
//
// Embedder 通常为用户的嵌入模型客户端(corellm.Client 满足 ragsearch.Embedder);
// Filter 用于把检索限定在评测语料(如某测试 user_id/kb_id)。
type SearcherRetriever struct {
	Searcher *ragsearch.Searcher[models.RAGChunkSource]
	Embedder ragsearch.Embedder
	Filter   vectorstore.Filter
}

// Retrieve 调 Searcher.Search 并把每条 Output 映射为评测视图 RetrievedHit。
func (s SearcherRetriever) Retrieve(ctx context.Context, query string, topK int) ([]rag.RetrievedHit, error) {
	opts := []ragsearch.InputOption{
		ragsearch.WithTopK(topK),
		ragsearch.WithFilters(s.Filter),
	}
	if s.Embedder != nil {
		opts = append(opts, ragsearch.WithInputEmbedder(s.Embedder))
	}
	res, err := s.Searcher.Search(ctx, query, opts...)
	if err != nil {
		return nil, err
	}
	hits := make([]rag.RetrievedHit, 0, len(res.Results))
	for _, o := range res.Results {
		hits = append(hits, rag.RetrievedHit{
			ChunkID:     o.ID,
			DocID:       o.Source.SourceID.String(),
			DocName:     o.Source.Name,
			Content:     o.Content,
			Score:       o.Score,
			RerankScore: o.RerankScore,
		})
	}
	return hits, nil
}

var _ rag.Retriever = SearcherRetriever{}
