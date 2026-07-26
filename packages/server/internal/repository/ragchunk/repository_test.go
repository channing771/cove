package ragchunk

import (
	"context"
	"testing"

	ragchunker "github.com/boxify/api-go/internal/core/rag/chunker"
	"github.com/boxify/api-go/internal/core/rag/vectorstore"
	"github.com/boxify/api-go/internal/infrastructure/db/memory"
	"github.com/boxify/api-go/internal/models"
	"github.com/google/uuid"
)

func sourceFilterFor(sourceID uuid.UUID) vectorstore.Filter {
	return vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("source_id", sourceID.String())}}
}

// 验证文档索引会同时写入稠密与关键词两个存储，并正确区分父/子块字段。
func TestRepositoryIndexDocumentChunksFansOutToBothStores(t *testing.T) {
	ctx := context.Background()
	mem := memory.New()
	repo := NewRepository(mem.Dense(), mem.Keyword())

	userID := uuid.New()
	docID := uuid.New()
	kbID := uuid.New()
	doc := &models.Document{ID: docID, UserID: userID, KBID: &kbID, FileName: "note.txt", SourceType: "file"}
	chunks := []*ragchunker.Chunk{{Content: "parent body", Children: []string{"child body"}}}
	vectors := [][]float64{{0.1, 0.2}, {0.3, 0.4}}

	if err := repo.EnsureIndex(ctx, 2); err != nil {
		t.Fatalf("EnsureIndex() error = %v", err)
	}
	if err := repo.IndexDocumentChunks(ctx, doc, chunks, vectors); err != nil {
		t.Fatalf("IndexDocumentChunks() error = %v", err)
	}

	filter := sourceFilterFor(docID)

	keywordHits, err := mem.Keyword().Fetch(ctx, filter, 10)
	if err != nil {
		t.Fatalf("keyword Fetch() error = %v", err)
	}
	if len(keywordHits) != 2 {
		t.Fatalf("keyword store chunk count = %d, want 2", len(keywordHits))
	}

	denseHits, err := mem.Dense().Search(ctx, []float64{0.1, 0.2}, 10, filter)
	if err != nil {
		t.Fatalf("dense Search() error = %v", err)
	}
	if len(denseHits) != 2 {
		t.Fatalf("dense store chunk count = %d, want 2", len(denseHits))
	}

	// 父块无 parent_id，子块指向父块。
	var parents, children int
	for _, hit := range keywordHits {
		if hit.Fields["level"] == "child" {
			children++
			if hit.Fields["parent_id"] == nil || hit.Fields["parent_id"] == "" {
				t.Fatalf("child chunk missing parent_id: %#v", hit.Fields)
			}
		} else {
			parents++
			if _, ok := hit.Fields["parent_id"]; ok {
				t.Fatalf("parent chunk should not carry parent_id: %#v", hit.Fields)
			}
		}
		if hit.Fields["kb_id"] != kbID.String() {
			t.Fatalf("kb_id = %v, want %s", hit.Fields["kb_id"], kbID.String())
		}
	}
	if parents != 1 || children != 1 {
		t.Fatalf("parents/children = %d/%d, want 1/1", parents, children)
	}
}

// 验证删除会同时清空两个存储。
func TestRepositoryDeleteBySourceRemovesFromBothStores(t *testing.T) {
	ctx := context.Background()
	mem := memory.New()
	repo := NewRepository(mem.Dense(), mem.Keyword())

	userID := uuid.New()
	docID := uuid.New()
	doc := &models.Document{ID: docID, UserID: userID, FileName: "n.txt", SourceType: "file"}
	chunks := []*ragchunker.Chunk{{Content: "hello"}}
	if err := repo.IndexDocumentChunks(ctx, doc, chunks, [][]float64{{1, 0}}); err != nil {
		t.Fatalf("IndexDocumentChunks() error = %v", err)
	}

	if err := repo.DeleteBySource(ctx, userID, docID); err != nil {
		t.Fatalf("DeleteBySource() error = %v", err)
	}

	filter := sourceFilterFor(docID)
	keywordHits, _ := mem.Keyword().Fetch(ctx, filter, 10)
	denseHits, _ := mem.Dense().Search(ctx, []float64{1, 0}, 10, filter)
	if len(keywordHits) != 0 || len(denseHits) != 0 {
		t.Fatalf("post-delete counts keyword=%d dense=%d, want 0/0", len(keywordHits), len(denseHits))
	}
}

// 验证更新标签会同步到两个存储。
func TestRepositoryUpdateTagsSyncsBothStores(t *testing.T) {
	ctx := context.Background()
	mem := memory.New()
	repo := NewRepository(mem.Dense(), mem.Keyword())

	userID := uuid.New()
	docID := uuid.New()
	doc := &models.Document{ID: docID, UserID: userID, FileName: "n.txt", SourceType: "file"}
	if err := repo.IndexDocumentChunks(ctx, doc, []*ragchunker.Chunk{{Content: "hi"}}, [][]float64{{1, 0}}); err != nil {
		t.Fatalf("IndexDocumentChunks() error = %v", err)
	}

	if err := repo.UpdateTags(ctx, userID, docID, []string{"work", "urgent"}); err != nil {
		t.Fatalf("UpdateTags() error = %v", err)
	}

	// tags 过滤命中即证明两个存储都已更新。
	filter := vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.In("tags", []string{"urgent"})}}
	keywordHits, _ := mem.Keyword().Fetch(ctx, filter, 10)
	denseHits, _ := mem.Dense().Search(ctx, []float64{1, 0}, 10, filter)
	if len(keywordHits) != 1 || len(denseHits) != 1 {
		t.Fatalf("tag-filtered counts keyword=%d dense=%d, want 1/1", len(keywordHits), len(denseHits))
	}
}

// 验证 DecodeSource 把命中字段解码成业务来源。
func TestRepositoryDecodeSource(t *testing.T) {
	repo := NewRepository(memory.New().Dense(), memory.New().Keyword())
	chunkID := uuid.New()
	sourceID := uuid.New()
	kbID := uuid.New()
	got, err := repo.DecodeSource(map[string]any{
		"chunk_id":    chunkID.String(),
		"source_id":   sourceID.String(),
		"kb_id":       kbID.String(),
		"name":        "note.txt",
		"source_type": "document",
	})
	if err != nil {
		t.Fatalf("DecodeSource() error = %v", err)
	}
	if got.ChunkID != chunkID || got.SourceID != sourceID || got.KBID == nil || *got.KBID != kbID {
		t.Fatalf("DecodeSource() = %#v", got)
	}
	if got.Name != "note.txt" || got.SourceType != "document" {
		t.Fatalf("DecodeSource() name/type = %q/%q", got.Name, got.SourceType)
	}
}
