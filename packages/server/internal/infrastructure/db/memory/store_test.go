package memory

import (
	"context"
	"testing"

	"github.com/boxify/api-go/internal/core/rag/vectorstore"
)

func point(id string, vector []float64, fields map[string]any) vectorstore.Point {
	return vectorstore.Point{ID: id, Vector: vector, Fields: fields}
}

// 验证稠密检索按 cosine 排序并遵守过滤条件。
func TestStoreDenseSearchRanksByCosineAndFilters(t *testing.T) {
	ctx := context.Background()
	store := New()
	_ = store.Upsert(ctx, []vectorstore.Point{
		point("a", []float64{1, 0}, map[string]any{"user_id": "u1", "content": "alpha"}),
		point("b", []float64{0, 1}, map[string]any{"user_id": "u1", "content": "beta"}),
		point("c", []float64{1, 0}, map[string]any{"user_id": "u2", "content": "gamma"}),
	})

	filter := vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("user_id", "u1")}}
	hits, err := store.Dense().Search(ctx, []float64{1, 0}, 10, filter)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2 (filtered to u1)", len(hits))
	}
	if hits[0].ID != "a" {
		t.Fatalf("top hit = %s, want a (highest cosine)", hits[0].ID)
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("scores not descending: %v", hits)
	}
}

// 验证关键词检索按 content 词项重合度打分，不含查询词的文档被排除。
func TestStoreKeywordSearchOverlap(t *testing.T) {
	ctx := context.Background()
	store := New()
	_ = store.Index(ctx, []vectorstore.Point{
		point("a", nil, map[string]any{"content": "the quick brown fox"}),
		point("b", nil, map[string]any{"content": "lazy dog sleeps"}),
	})

	hits, err := store.Keyword().Search(ctx, "quick fox", 10, vectorstore.Filter{})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "a" {
		t.Fatalf("hits = %#v, want only a", hits)
	}
}

// 验证 In 命中列表字段（tags）以及 MustNot 反向过滤。
func TestStoreFilterInAndMustNot(t *testing.T) {
	ctx := context.Background()
	store := New()
	_ = store.Upsert(ctx, []vectorstore.Point{
		point("a", []float64{1}, map[string]any{"tags": []string{"work", "urgent"}, "source_type": "document", "content": "x"}),
		point("b", []float64{1}, map[string]any{"tags": []string{"home"}, "source_type": "image", "content": "x"}),
	})

	in := vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.In("tags", []string{"urgent"})}}
	hits, _ := store.Dense().Search(ctx, []float64{1}, 10, in)
	if len(hits) != 1 || hits[0].ID != "a" {
		t.Fatalf("In(tags) hits = %#v, want only a", hits)
	}

	notImage := vectorstore.Filter{MustNot: []vectorstore.Condition{vectorstore.Eq("source_type", "image")}}
	hits, _ = store.Dense().Search(ctx, []float64{1}, 10, notImage)
	if len(hits) != 1 || hits[0].ID != "a" {
		t.Fatalf("MustNot(image) hits = %#v, want only a", hits)
	}
}

// 验证 SetFields 更新后可被新条件命中，Fetch 遵守过滤与 size。
func TestStoreSetFieldsAndFetch(t *testing.T) {
	ctx := context.Background()
	store := New()
	_ = store.Upsert(ctx, []vectorstore.Point{
		point("a", []float64{1}, map[string]any{"source_id": "s1", "content": "x"}),
	})

	if err := store.SetFields(ctx, vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("source_id", "s1")}}, map[string]any{"kb_id": "kb9"}); err != nil {
		t.Fatalf("SetFields() error = %v", err)
	}
	hits, _ := store.Fetch(ctx, vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("kb_id", "kb9")}}, 10)
	if len(hits) != 1 {
		t.Fatalf("Fetch after SetFields = %d, want 1", len(hits))
	}
}
