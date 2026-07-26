// Package memory 提供 vectorstore.DenseIndex 与 vectorstore.KeywordIndex 的内存实现。
//
// 稠密向量与关键词文档共用同一份内存映射，适合本地开发与测试；生产环境应使用
// Qdrant / Elasticsearch 适配器。由于两个端口都有 Search 方法但签名不同，
// 单个类型无法同时实现两者，因此 Store 通过 Dense() / Keyword() 暴露两个共享底层数据的视图。
package memory

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/boxify/api-go/internal/core/rag/vectorstore"
	"github.com/boxify/api-go/internal/core/valuex"
	"github.com/boxify/api-go/internal/util"
)

// Store 是稠密 + 关键词共享的内存存储。
type Store struct {
	mu     sync.Mutex
	points map[string]vectorstore.Point
}

// New 创建空的内存存储。
func New() *Store {
	return &Store{points: make(map[string]vectorstore.Point)}
}

// Dense 返回实现 vectorstore.DenseIndex 的视图。
func (s *Store) Dense() vectorstore.DenseIndex { return denseView{s} }

// Keyword 返回实现 vectorstore.KeywordIndex 的视图。
func (s *Store) Keyword() vectorstore.KeywordIndex { return keywordView{s} }

// --- 共享写入/维护实现 ---

// EnsureCollection 内存实现无需建库。
func (s *Store) EnsureCollection(ctx context.Context, dim int) error { return nil }

// EnsureIndex 内存实现无需建索引。
func (s *Store) EnsureIndex(ctx context.Context) error { return nil }

// Upsert 按 ID 幂等写入向量点。
func (s *Store) Upsert(ctx context.Context, points []vectorstore.Point) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, point := range points {
		if point.ID == "" {
			continue
		}
		s.points[point.ID] = clonePoint(point)
	}
	return nil
}

// Index 写入文档（与 Upsert 共用底层存储）。
func (s *Store) Index(ctx context.Context, docs []vectorstore.Point) error {
	return s.Upsert(ctx, docs)
}

// DeleteByFilter 删除命中过滤条件的全部记录。
func (s *Store) DeleteByFilter(ctx context.Context, filter vectorstore.Filter) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, point := range s.points {
		if matchesFilter(point.Fields, filter) {
			delete(s.points, id)
		}
	}
	return nil
}

// SetFields 为命中过滤条件的记录批量更新字段。
func (s *Store) SetFields(ctx context.Context, filter vectorstore.Filter, fields map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, point := range s.points {
		if !matchesFilter(point.Fields, filter) {
			continue
		}
		updated := cloneFields(point.Fields)
		for key, value := range fields {
			updated[key] = value
		}
		point.Fields = updated
		s.points[id] = point
	}
	return nil
}

// Fetch 按过滤条件取回文档，不做相关度排序。
func (s *Store) Fetch(ctx context.Context, filter vectorstore.Filter, size int) ([]vectorstore.Hit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hits := make([]vectorstore.Hit, 0)
	for _, point := range s.points {
		if !matchesFilter(point.Fields, filter) {
			continue
		}
		hits = append(hits, vectorstore.Hit{ID: point.ID, Fields: cloneFields(point.Fields)})
		if size > 0 && len(hits) >= size {
			break
		}
	}
	return hits, nil
}

func (s *Store) denseSearch(vector []float64, k int, filter vectorstore.Filter) []vectorstore.Hit {
	s.mu.Lock()
	defer s.mu.Unlock()
	hits := make([]vectorstore.Hit, 0)
	for _, point := range s.points {
		if !matchesFilter(point.Fields, filter) {
			continue
		}
		hits = append(hits, vectorstore.Hit{
			ID:     point.ID,
			Score:  util.Cosine(vector, point.Vector),
			Fields: cloneFields(point.Fields),
		})
	}
	return topK(hits, k)
}

func (s *Store) keywordSearch(query string, k int, filter vectorstore.Filter) []vectorstore.Hit {
	s.mu.Lock()
	defer s.mu.Unlock()
	terms := tokenize(query)
	hits := make([]vectorstore.Hit, 0)
	for _, point := range s.points {
		if !matchesFilter(point.Fields, filter) {
			continue
		}
		score := overlapScore(terms, valuex.String(point.Fields["content"]))
		if score <= 0 {
			continue
		}
		hits = append(hits, vectorstore.Hit{
			ID:     point.ID,
			Score:  score,
			Fields: cloneFields(point.Fields),
		})
	}
	return topK(hits, k)
}

// denseView 暴露 DenseIndex（Search 接受向量）。
type denseView struct{ *Store }

func (d denseView) Search(ctx context.Context, vector []float64, k int, filter vectorstore.Filter) ([]vectorstore.Hit, error) {
	return d.Store.denseSearch(vector, k, filter), nil
}

// keywordView 暴露 KeywordIndex（Search 接受查询词）。
type keywordView struct{ *Store }

func (k keywordView) Search(ctx context.Context, query string, size int, filter vectorstore.Filter) ([]vectorstore.Hit, error) {
	return k.Store.keywordSearch(query, size, filter), nil
}

// matchesFilter 判断字段是否满足中立过滤条件。
func matchesFilter(fields map[string]any, filter vectorstore.Filter) bool {
	for _, condition := range filter.Must {
		if !matchesCondition(fields, condition) {
			return false
		}
	}
	for _, condition := range filter.MustNot {
		if matchesCondition(fields, condition) {
			return false
		}
	}
	return true
}

func matchesCondition(fields map[string]any, condition vectorstore.Condition) bool {
	stored := fields[condition.Field]
	switch condition.Op {
	case vectorstore.OpIn:
		wanted := toStringSet(condition.Value)
		for _, value := range fieldValues(stored) {
			if _, ok := wanted[value]; ok {
				return true
			}
		}
		return false
	default:
		want := valuex.String(condition.Value)
		for _, value := range fieldValues(stored) {
			if value == want {
				return true
			}
		}
		return false
	}
}

// fieldValues 把存储字段值展开为字符串切片（标量或列表）。
func fieldValues(stored any) []string {
	switch v := stored.(type) {
	case nil:
		return nil
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, valuex.String(item))
		}
		return out
	default:
		return []string{valuex.String(v)}
	}
}

func toStringSet(value any) map[string]struct{} {
	out := make(map[string]struct{})
	switch v := value.(type) {
	case []string:
		for _, item := range v {
			out[item] = struct{}{}
		}
	case []any:
		for _, item := range v {
			out[valuex.String(item)] = struct{}{}
		}
	default:
		out[valuex.String(v)] = struct{}{}
	}
	return out
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r < 128
	})
}

// overlapScore 计算查询词项在内容中出现的数量，作为简化关键词分。
func overlapScore(terms []string, content string) float64 {
	if len(terms) == 0 {
		return 0
	}
	lower := strings.ToLower(content)
	score := 0.0
	for _, term := range terms {
		if term != "" && strings.Contains(lower, term) {
			score++
		}
	}
	return score
}

func topK(hits []vectorstore.Hit, k int) []vectorstore.Hit {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].ID < hits[j].ID
		}
		return hits[i].Score > hits[j].Score
	})
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return hits
}

func clonePoint(point vectorstore.Point) vectorstore.Point {
	vector := make([]float64, len(point.Vector))
	copy(vector, point.Vector)
	return vectorstore.Point{ID: point.ID, Vector: vector, Fields: cloneFields(point.Fields)}
}

func cloneFields(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields))
	for key, value := range fields {
		out[key] = value
	}
	return out
}

var (
	_ vectorstore.DenseIndex   = denseView{}
	_ vectorstore.KeywordIndex = keywordView{}
)
