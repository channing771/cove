package corpus

import (
	"context"
	"fmt"
	"strings"

	ragchunker "github.com/boxify/api-go/internal/core/rag/chunker"
	"github.com/boxify/api-go/internal/core/rag/vectorstore"
	"github.com/boxify/api-go/internal/repository"
	"github.com/boxify/api-go/internal/repository/ragchunk"
)

// BatchEmbedder 批量嵌入文本;HashEmbedder 与真实嵌入客户端均可满足。
type BatchEmbedder interface {
	Embed(ctx context.Context, texts []string, dimensions int) ([][]float64, error)
}

// Ingester 用生产链路把语料写入检索存储:
// 真实分块器 ragchunker → 嵌入 → ragchunk.Repository 双写 DenseIndex + KeywordIndex。
type Ingester struct {
	Dense    vectorstore.DenseIndex
	Keyword  vectorstore.KeywordIndex
	Chunker  *ragchunker.Chunker
	Embedder BatchEmbedder
	Dim      int
}

// Ingest 建索引并写入全部语料,返回写入的 chunk 总数。
//
// 会先 EnsureCollection/EnsureIndex,再按文档逐篇 DeleteBySource 清旧后重灌,使重复运行
// 幂等(chunk id 由文档 id 确定性派生)。
func (in *Ingester) Ingest(ctx context.Context, docs []Doc) (int, error) {
	if in.Dense == nil || in.Keyword == nil {
		return 0, fmt.Errorf("corpus: dense/keyword index is nil")
	}
	if in.Embedder == nil {
		return 0, fmt.Errorf("corpus: embedder is nil")
	}
	chunker := in.Chunker
	if chunker == nil {
		chunker = ragchunker.NewChunker()
	}
	if err := in.Dense.EnsureCollection(ctx, in.Dim); err != nil {
		return 0, fmt.Errorf("ensure dense collection: %w", err)
	}
	if err := in.Keyword.EnsureIndex(ctx); err != nil {
		return 0, fmt.Errorf("ensure keyword index: %w", err)
	}

	repo := ragchunk.NewRepository(in.Dense, in.Keyword)
	total := 0
	for _, doc := range docs {
		chunks := chunker.Chunk(doc.Content)
		texts := chunkTexts(chunks)
		if len(texts) == 0 {
			continue
		}
		vectors, err := in.Embedder.Embed(ctx, texts, in.Dim)
		if err != nil {
			return total, fmt.Errorf("embed %s: %w", doc.Name, err)
		}
		if err := in.reindex(ctx, repo, doc, chunks, vectors); err != nil {
			return total, err
		}
		total += len(texts)
	}
	return total, nil
}

// reindex 清掉该文档旧 chunk 后重新写入,保证重复摄入幂等。
func (in *Ingester) reindex(ctx context.Context, repo repository.RAGChunkRepository, doc Doc, chunks []*ragchunker.Chunk, vectors [][]float64) error {
	if err := repo.DeleteBySource(ctx, doc.Document.UserID, doc.Document.ID); err != nil {
		return fmt.Errorf("delete old chunks of %s: %w", doc.Name, err)
	}
	if err := repo.IndexDocumentChunks(ctx, doc.Document, chunks, vectors); err != nil {
		return fmt.Errorf("index %s: %w", doc.Name, err)
	}
	return nil
}

// chunkTexts 复刻生产写入路径的文本顺序契约:每个父块后紧跟其子块,跳过空白。
//
// 必须与 ragchunk 内部顺序一致,否则向量会与 chunk 错位。
func chunkTexts(chunks []*ragchunker.Chunk) []string {
	texts := make([]string, 0)
	for _, parent := range chunks {
		if parent == nil {
			continue
		}
		if content := strings.TrimSpace(parent.Content); content != "" {
			texts = append(texts, content)
		}
		for _, child := range parent.Children {
			if content := strings.TrimSpace(child); content != "" {
				texts = append(texts, content)
			}
		}
	}
	return texts
}
