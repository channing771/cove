package es

import (
	"context"
	"fmt"
	"strings"

	"github.com/boxify/api-go/internal/core/rag/vectorstore"
	"github.com/boxify/api-go/internal/core/valuex"
)

// KeywordIndex 用 Elasticsearch 实现 vectorstore.KeywordIndex（BM25 关键词召回 + 文档存储）。
//
// 它把中立的 vectorstore.Filter 翻译成 ES 查询 DSL，把 ES 命中翻译回中立 vectorstore.Hit，
// 使 core 检索层无需了解任何 ES 细节。稠密向量不落入本存储。
type KeywordIndex struct {
	client *Client
	index  string
}

// NewKeywordIndex 创建 ES 关键词索引适配器。
func NewKeywordIndex(client *Client, index string) *KeywordIndex {
	if client == nil {
		panic("elasticsearch client is required")
	}
	index = strings.TrimSpace(index)
	if index == "" {
		index = "cove_chunks"
	}
	return &KeywordIndex{client: client, index: index}
}

// EnsureIndex 确保关键词索引存在（不含向量字段，向量由稠密存储负责）。
func (k *KeywordIndex) EnsureIndex(ctx context.Context) error {
	exists, err := k.client.IndexExists(ctx, k.index)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = k.client.CreateIndex(ctx, k.index, keywordIndexMapping())
	return err
}

// Index 写入若干文档，Point.Vector 被忽略。
func (k *KeywordIndex) Index(ctx context.Context, docs []vectorstore.Point) error {
	for _, doc := range docs {
		if doc.ID == "" {
			continue
		}
		if _, err := k.client.Index(ctx, k.index, doc.ID, doc.Fields); err != nil {
			return err
		}
	}
	return nil
}

// DeleteByFilter 删除命中过滤条件的全部文档。
func (k *KeywordIndex) DeleteByFilter(ctx context.Context, filter vectorstore.Filter) error {
	_, err := k.client.DeleteByQuery(ctx, k.index, map[string]any{
		"query": boolQuery(filter, nil),
	})
	return err
}

// SetFields 为命中过滤条件的文档批量更新给定字段。
func (k *KeywordIndex) SetFields(ctx context.Context, filter vectorstore.Filter, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	var source strings.Builder
	params := make(map[string]any, len(fields))
	for field, value := range fields {
		source.WriteString(fmt.Sprintf("ctx._source.%s = params.%s;", field, field))
		params[field] = value
	}
	_, err := k.client.UpdateByQuery(ctx, k.index, map[string]any{
		"script": map[string]any{
			"source": source.String(),
			"params": params,
		},
		"query": boolQuery(filter, nil),
	})
	return err
}

// Search 执行 BM25 关键词召回。
func (k *KeywordIndex) Search(ctx context.Context, query string, size int, filter vectorstore.Filter) ([]vectorstore.Hit, error) {
	must := []any{map[string]any{"match": map[string]any{"content": query}}}
	resp, err := k.client.Search(ctx, k.index, map[string]any{
		"size":  size,
		"query": boolQuery(filter, must),
	})
	if err != nil {
		return nil, err
	}
	return decodeHits(resp), nil
}

// Fetch 按过滤条件直接取回文档，不做相关度排序。
func (k *KeywordIndex) Fetch(ctx context.Context, filter vectorstore.Filter, size int) ([]vectorstore.Hit, error) {
	resp, err := k.client.Search(ctx, k.index, map[string]any{
		"size":  size,
		"query": boolQuery(filter, nil),
	})
	if err != nil {
		return nil, err
	}
	return decodeHits(resp), nil
}

// boolQuery 把中立 Filter（外加可选 must 子句）翻译成 ES bool query。
func boolQuery(filter vectorstore.Filter, must []any) map[string]any {
	boolBody := map[string]any{}
	if len(must) > 0 {
		boolBody["must"] = must
	}
	if clauses := esConditions(filter.Must); len(clauses) > 0 {
		boolBody["filter"] = clauses
	}
	if clauses := esConditions(filter.MustNot); len(clauses) > 0 {
		boolBody["must_not"] = clauses
	}
	return map[string]any{"bool": boolBody}
}

// esConditions 把中立谓词翻译成 ES term/terms 子句。
func esConditions(conditions []vectorstore.Condition) []any {
	if len(conditions) == 0 {
		return nil
	}
	out := make([]any, 0, len(conditions))
	for _, condition := range conditions {
		switch condition.Op {
		case vectorstore.OpIn:
			out = append(out, map[string]any{"terms": map[string]any{condition.Field: condition.Value}})
		default:
			out = append(out, map[string]any{"term": map[string]any{condition.Field: condition.Value}})
		}
	}
	return out
}

// decodeHits 把 ES 响应翻译成中立 Hit。
func decodeHits(resp map[string]any) []vectorstore.Hit {
	hitsObj, _ := resp["hits"].(map[string]any)
	rawHits, _ := hitsObj["hits"].([]any)
	out := make([]vectorstore.Hit, 0, len(rawHits))
	for _, raw := range rawHits {
		hit, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := valuex.String(hit["_id"])
		if id == "" {
			continue
		}
		src, _ := hit["_source"].(map[string]any)
		if src == nil {
			src = map[string]any{}
		}
		out = append(out, vectorstore.Hit{
			ID:     id,
			Score:  valuex.Float(hit["_score"]),
			Fields: src,
		})
	}
	return out
}

// keywordIndexMapping 关键词索引映射（不含向量字段）。
func keywordIndexMapping() map[string]any {
	return map[string]any{
		"mappings": map[string]any{
			"properties": map[string]any{
				"chunk_id":    map[string]any{"type": "keyword"},
				"parent_id":   map[string]any{"type": "keyword"},
				"source_id":   map[string]any{"type": "keyword"},
				"user_id":     map[string]any{"type": "keyword"},
				"kb_id":       map[string]any{"type": "keyword"},
				"name":        map[string]any{"type": "keyword"},
				"source_type": map[string]any{"type": "keyword"},
				"level":       map[string]any{"type": "keyword"},
				"tags":        map[string]any{"type": "keyword"},
				"content":     map[string]any{"type": "text"},
			},
		},
	}
}

var _ vectorstore.KeywordIndex = (*KeywordIndex)(nil)
