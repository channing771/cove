// Package ragchunk 提供 RAG chunk 的写入编排：把文档/图片切块后的记录同时写入
// 稠密向量存储（DenseIndex）与关键词存储（KeywordIndex），并向下游提供命中字段解码。
//
// 本包只依赖 vectorstore 中立端口，不绑定任何具体数据库。
package ragchunk

import (
	"context"
	"fmt"
	"strings"

	ragchunker "github.com/boxify/api-go/internal/core/rag/chunker"
	"github.com/boxify/api-go/internal/core/rag/vectorstore"
	"github.com/boxify/api-go/internal/core/valuex"
	"github.com/boxify/api-go/internal/models"
	"github.com/boxify/api-go/internal/repository"
	"github.com/boxify/api-go/internal/xerr"
	"github.com/google/uuid"
)

// SourceTypeImage 表示 chunk 来源为图片。
const SourceTypeImage = "image"

// Repository 把 chunk 记录扇出到稠密与关键词两个存储。
type Repository struct {
	dense   vectorstore.DenseIndex
	keyword vectorstore.KeywordIndex
}

// NewRepository 创建 chunk 写入编排器。
func NewRepository(dense vectorstore.DenseIndex, keyword vectorstore.KeywordIndex) repository.RAGChunkRepository {
	if dense == nil || keyword == nil {
		panic("dense and keyword stores are required")
	}
	return &Repository{dense: dense, keyword: keyword}
}

// EnsureIndex 确保两个存储的集合/索引均存在。
func (r *Repository) EnsureIndex(ctx context.Context, embeddingDim int) error {
	if embeddingDim <= 0 {
		embeddingDim = 1024
	}
	if err := r.dense.EnsureCollection(ctx, embeddingDim); err != nil {
		return err
	}
	return r.keyword.EnsureIndex(ctx)
}

// IndexDocumentChunks 把文档 chunk 写入稠密与关键词存储。
func (r *Repository) IndexDocumentChunks(ctx context.Context, doc *models.Document, chunks []*ragchunker.Chunk, vectors [][]float64) error {
	records, err := buildDocumentChunkRecords(doc, chunks, vectors)
	if err != nil {
		return err
	}
	return r.writeRecords(ctx, records)
}

// IndexImageChunk 把图片描述 chunk 写入稠密与关键词存储。
func (r *Repository) IndexImageChunk(ctx context.Context, image *models.Image, content string, vector []float64) error {
	if image == nil {
		return xerr.Internal("图片为空", nil)
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return xerr.Internal("图片描述内容为空", nil)
	}
	if len(vector) == 0 {
		return xerr.Internal("图片向量为空", nil)
	}
	kbID := ""
	if image.KBID != nil {
		kbID = image.KBID.String()
	}
	record := models.RAGChunkRecord{
		ChunkID:    deterministicImageChunkID(image.ID).String(),
		SourceID:   image.ID.String(),
		UserID:     image.UserID.String(),
		KBID:       kbID,
		Name:       image.FileName,
		SourceType: SourceTypeImage,
		Content:    content,
		Level:      "parent",
		Tags:       tagNames(image.Tags),
		Vector:     vector,
	}
	return r.writeRecords(ctx, []models.RAGChunkRecord{record})
}

// DeleteBySource 按来源实体 ID 从两个存储删除 chunk。
func (r *Repository) DeleteBySource(ctx context.Context, userID uuid.UUID, sourceID uuid.UUID) error {
	filter := sourceFilter(userID, sourceID)
	if err := r.dense.DeleteByFilter(ctx, filter); err != nil {
		return err
	}
	return r.keyword.DeleteByFilter(ctx, filter)
}

// UpdateKnowledgeBase 更新来源 chunk 的知识库归属（两个存储同步）。
func (r *Repository) UpdateKnowledgeBase(ctx context.Context, userID uuid.UUID, sourceID uuid.UUID, kbID uuid.UUID) error {
	filter := sourceFilter(userID, sourceID)
	fields := map[string]any{"kb_id": kbID.String()}
	if err := r.dense.SetFields(ctx, filter, fields); err != nil {
		return err
	}
	return r.keyword.SetFields(ctx, filter, fields)
}

// UpdateTags 更新来源 chunk 的标签（两个存储同步）。
func (r *Repository) UpdateTags(ctx context.Context, userID uuid.UUID, sourceID uuid.UUID, tags []string) error {
	filter := sourceFilter(userID, sourceID)
	fields := map[string]any{"tags": tags}
	if err := r.dense.SetFields(ctx, filter, fields); err != nil {
		return err
	}
	return r.keyword.SetFields(ctx, filter, fields)
}

// DecodeSource 把命中字段解码成业务来源元数据。
func (r *Repository) DecodeSource(src map[string]any) (models.RAGChunkSource, error) {
	chunkID, err := uuid.Parse(valuex.String(src["chunk_id"]))
	if err != nil {
		return models.RAGChunkSource{}, fmt.Errorf("invalid chunk_id: %w", err)
	}
	sourceID, err := uuid.Parse(valuex.String(src["source_id"]))
	if err != nil {
		return models.RAGChunkSource{}, fmt.Errorf("invalid source_id: %w", err)
	}
	var kbID *uuid.UUID
	if rawKBID := valuex.String(src["kb_id"]); rawKBID != "" {
		parsed, err := uuid.Parse(rawKBID)
		if err != nil {
			return models.RAGChunkSource{}, fmt.Errorf("invalid kb_id: %w", err)
		}
		kbID = &parsed
	}
	return models.RAGChunkSource{
		ChunkID:    chunkID,
		SourceID:   sourceID,
		KBID:       kbID,
		Name:       valuex.String(src["name"]),
		SourceType: valuex.String(src["source_type"]),
	}, nil
}

// writeRecords 把记录转换成中立 Point 并写入稠密与关键词两个存储。
func (r *Repository) writeRecords(ctx context.Context, records []models.RAGChunkRecord) error {
	if len(records) == 0 {
		return nil
	}
	points := make([]vectorstore.Point, 0, len(records))
	for _, record := range records {
		points = append(points, recordPoint(record))
	}
	// 先写稠密向量再写关键词；任一失败整体返回错误，交由上层重试。
	if err := r.dense.Upsert(ctx, points); err != nil {
		return err
	}
	return r.keyword.Index(ctx, points)
}

// recordPoint 把 chunk 记录转换成中立 Point（向量单列，其余字段进 payload/_source）。
func recordPoint(record models.RAGChunkRecord) vectorstore.Point {
	return vectorstore.Point{
		ID:     record.ChunkID,
		Vector: record.Vector,
		Fields: recordFields(record),
	}
}

// recordFields 构造命中字段，字段名与检索/解码侧一致（不含向量）。
func recordFields(record models.RAGChunkRecord) map[string]any {
	fields := map[string]any{
		"chunk_id":    record.ChunkID,
		"source_id":   record.SourceID,
		"user_id":     record.UserID,
		"name":        record.Name,
		"source_type": record.SourceType,
		"content":     record.Content,
		"level":       record.Level,
		"tags":        record.Tags,
	}
	if record.ParentID != "" {
		fields["parent_id"] = record.ParentID
	}
	if record.KBID != "" {
		fields["kb_id"] = record.KBID
	}
	return fields
}

// sourceFilter 构造按用户 + 来源过滤的中立条件。
func sourceFilter(userID uuid.UUID, sourceID uuid.UUID) vectorstore.Filter {
	return vectorstore.Filter{Must: []vectorstore.Condition{
		vectorstore.Eq("user_id", userID.String()),
		vectorstore.Eq("source_id", sourceID.String()),
	}}
}

// buildDocumentChunkRecords 构建文档 chunk 写入记录（父块 + 子块）。
func buildDocumentChunkRecords(doc *models.Document, chunks []*ragchunker.Chunk, vectors [][]float64) ([]models.RAGChunkRecord, error) {
	if doc == nil {
		return nil, xerr.Internal("文档为空", nil)
	}
	texts := chunkTexts(chunks)
	if len(texts) != len(vectors) {
		return nil, xerr.Internal("文档 chunk 向量数量不匹配", nil)
	}
	tags := tagNames(doc.Tags)
	kbID := ""
	if doc.KBID != nil {
		kbID = doc.KBID.String()
	}
	out := make([]models.RAGChunkRecord, 0, len(texts))
	vectorIndex := 0
	for parentIndex, parent := range chunks {
		if parent == nil {
			continue
		}
		parentContent := strings.TrimSpace(parent.Content)
		parentID := deterministicChunkID(doc.ID, parentIndex, -1).String()
		if parentContent != "" {
			out = append(out, models.RAGChunkRecord{
				ChunkID:    parentID,
				SourceID:   doc.ID.String(),
				UserID:     doc.UserID.String(),
				KBID:       kbID,
				Name:       doc.FileName,
				SourceType: doc.SourceType,
				Content:    parentContent,
				Level:      "parent",
				Tags:       tags,
				Vector:     vectors[vectorIndex],
			})
			vectorIndex++
		}
		for childIndex, child := range parent.Children {
			childContent := strings.TrimSpace(child)
			if childContent == "" {
				continue
			}
			childID := deterministicChunkID(doc.ID, parentIndex, childIndex).String()
			out = append(out, models.RAGChunkRecord{
				ChunkID:    childID,
				ParentID:   parentID,
				SourceID:   doc.ID.String(),
				UserID:     doc.UserID.String(),
				KBID:       kbID,
				Name:       doc.FileName,
				SourceType: doc.SourceType,
				Content:    childContent,
				Level:      "child",
				Tags:       tags,
				Vector:     vectors[vectorIndex],
			})
			vectorIndex++
		}
	}
	return out, nil
}

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

func tagNames(rows []models.Tag) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if name := strings.TrimSpace(row.Name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// deterministicChunkID 确定性 chunk ID。
func deterministicChunkID(documentID uuid.UUID, parentIndex int, childIndex int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("boxify:document:%s:%d:%d", documentID.String(), parentIndex, childIndex)))
}

// deterministicImageChunkID 生成图片描述 chunk 的确定性 ID。
func deterministicImageChunkID(imageID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("boxify:image:%s:0", imageID.String())))
}
