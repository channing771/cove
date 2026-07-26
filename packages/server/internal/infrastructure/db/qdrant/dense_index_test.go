package qdrant

import (
	"reflect"
	"testing"

	"github.com/boxify/api-go/internal/core/rag/vectorstore"
)

// 验证空 Filter 翻译为 nil（全量），非空则产生对应数量的 must/must_not 条件。
func TestQdrantFilterTranslation(t *testing.T) {
	if qdrantFilter(vectorstore.Filter{}) != nil {
		t.Fatalf("empty filter should translate to nil")
	}
	f := qdrantFilter(vectorstore.Filter{
		Must: []vectorstore.Condition{
			vectorstore.Eq("user_id", "u1"),
			vectorstore.In("tags", []string{"a", "b"}),
		},
		MustNot: []vectorstore.Condition{vectorstore.Eq("source_type", "image")},
	})
	if f == nil {
		t.Fatalf("non-empty filter should not be nil")
	}
	if len(f.Must) != 2 || len(f.MustNot) != 1 {
		t.Fatalf("must/mustNot = %d/%d, want 2/1", len(f.Must), len(f.MustNot))
	}
}

// 验证 payload 往返：写入字段（含 []string）后可无损读回为中立字段。
func TestPayloadRoundTrip(t *testing.T) {
	fields := map[string]any{
		"content":     "hello",
		"source_type": "document",
		"tags":        []string{"work", "urgent"},
	}
	payload, err := payloadFromFields(fields)
	if err != nil {
		t.Fatalf("payloadFromFields() error = %v", err)
	}
	got := payloadToFields(payload)

	if got["content"] != "hello" || got["source_type"] != "document" {
		t.Fatalf("scalar round-trip failed: %#v", got)
	}
	if !reflect.DeepEqual(got["tags"], []any{"work", "urgent"}) {
		t.Fatalf("list round-trip = %#v, want []any{work,urgent}", got["tags"])
	}
}

func TestToStrings(t *testing.T) {
	if got := toStrings([]string{"a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("toStrings([]string) = %#v", got)
	}
	if got := toStrings([]any{"a", 2}); !reflect.DeepEqual(got, []string{"a", "2"}) {
		t.Fatalf("toStrings([]any) = %#v", got)
	}
	if got := toStrings("solo"); !reflect.DeepEqual(got, []string{"solo"}) {
		t.Fatalf("toStrings(scalar) = %#v", got)
	}
}

func TestSplitAddr(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort int
	}{
		{"", "localhost", 6334},
		{"qdrant", "qdrant", 6334},
		{"qdrant:6334", "qdrant", 6334},
		{"10.0.0.1:7000", "10.0.0.1", 7000},
	}
	for _, tc := range cases {
		host, port, err := splitAddr(tc.in)
		if err != nil {
			t.Fatalf("splitAddr(%q) error = %v", tc.in, err)
		}
		if host != tc.wantHost || port != tc.wantPort {
			t.Fatalf("splitAddr(%q) = %s/%d, want %s/%d", tc.in, host, port, tc.wantHost, tc.wantPort)
		}
	}
	if _, _, err := splitAddr("host:notaport"); err == nil {
		t.Fatalf("splitAddr with bad port should error")
	}
}
