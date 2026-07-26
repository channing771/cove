package es

import (
	"reflect"
	"testing"

	"github.com/boxify/api-go/internal/core/rag/vectorstore"
)

// 验证中立谓词翻译成 ES term/terms 子句。
func TestESConditionsTranslatesEqAndIn(t *testing.T) {
	got := esConditions([]vectorstore.Condition{
		vectorstore.Eq("user_id", "u1"),
		vectorstore.In("tags", []string{"a", "b"}),
	})
	want := []any{
		map[string]any{"term": map[string]any{"user_id": "u1"}},
		map[string]any{"terms": map[string]any{"tags": []string{"a", "b"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("esConditions() = %#v, want %#v", got, want)
	}
	if esConditions(nil) != nil {
		t.Fatalf("esConditions(nil) should be nil")
	}
}

// 验证 boolQuery 组合额外 must、filter(Must) 与 must_not。
func TestBoolQueryCombinesClauses(t *testing.T) {
	filter := vectorstore.Filter{
		Must:    []vectorstore.Condition{vectorstore.Eq("user_id", "u1")},
		MustNot: []vectorstore.Condition{vectorstore.Eq("source_type", "image")},
	}
	must := []any{map[string]any{"match": map[string]any{"content": "hi"}}}
	got := boolQuery(filter, must)["bool"].(map[string]any)

	if !reflect.DeepEqual(got["must"], must) {
		t.Fatalf("must = %#v", got["must"])
	}
	if !reflect.DeepEqual(got["filter"], []any{map[string]any{"term": map[string]any{"user_id": "u1"}}}) {
		t.Fatalf("filter = %#v", got["filter"])
	}
	if !reflect.DeepEqual(got["must_not"], []any{map[string]any{"term": map[string]any{"source_type": "image"}}}) {
		t.Fatalf("must_not = %#v", got["must_not"])
	}
}

// 验证 ES 响应翻译成中立 Hit。
func TestDecodeHitsMapsIDScoreSource(t *testing.T) {
	resp := map[string]any{
		"hits": map[string]any{
			"hits": []any{
				map[string]any{"_id": "a", "_score": 1.5, "_source": map[string]any{"content": "x"}},
				map[string]any{"_id": "", "_score": 1.0, "_source": map[string]any{}}, // 空 id 跳过
				map[string]any{"_id": "b", "_score": 0.5},                             // 无 _source → 空字段
			},
		},
	}
	got := decodeHits(resp)
	if len(got) != 2 {
		t.Fatalf("decodeHits len = %d, want 2", len(got))
	}
	if got[0].ID != "a" || got[0].Score != 1.5 || got[0].Fields["content"] != "x" {
		t.Fatalf("hit[0] = %#v", got[0])
	}
	if got[1].ID != "b" || got[1].Fields == nil {
		t.Fatalf("hit[1] = %#v, want non-nil fields", got[1])
	}
}
