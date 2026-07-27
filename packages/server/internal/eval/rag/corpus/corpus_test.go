package corpus

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestHashEmbedderDeterministicAndNormalized(t *testing.T) {
	e := NewHashEmbedder(256)
	ctx := context.Background()
	a, err := e.EmbedOne(ctx, "向量数据库存的是向量", 0)
	if err != nil {
		t.Fatalf("EmbedOne: %v", err)
	}
	b, _ := e.EmbedOne(ctx, "向量数据库存的是向量", 0)
	if len(a) != 256 {
		t.Fatalf("dim = %d, want 256", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("not deterministic at %d: %v vs %v", i, a[i], b[i])
		}
	}
	norm := 0.0
	for _, v := range a {
		norm += v * v
	}
	if math.Abs(norm-1) > 1e-9 {
		t.Fatalf("L2 norm = %v, want 1", norm)
	}
}

// 相近文本的余弦相似度应高于不相关文本 —— 保证向量通道真的携带词形信号。
func TestHashEmbedderSimilarityOrdering(t *testing.T) {
	e := NewHashEmbedder(512)
	ctx := context.Background()
	q, _ := e.EmbedOne(ctx, "向量存储抽象定义了哪两个端口", 0)
	near, _ := e.EmbedOne(ctx, "向量存储抽象把端口分为稠密索引与关键词索引", 0)
	far, _ := e.EmbedOne(ctx, "Langfuse 本地部署与 OTLP 采样率配置", 0)
	if cosine(q, near) <= cosine(q, far) {
		t.Fatalf("similar text must score higher: near=%v far=%v", cosine(q, near), cosine(q, far))
	}
}

func cosine(a, b []float64) float64 {
	dot := 0.0
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}

func TestHashEmbedderEmptyText(t *testing.T) {
	v, err := NewHashEmbedder(64).EmbedOne(context.Background(), "", 0)
	if err != nil || len(v) != 64 {
		t.Fatalf("empty text should yield zero vector of dim 64: %v %v", len(v), err)
	}
	for _, x := range v {
		if x != 0 {
			t.Fatal("empty text must produce zero vector")
		}
	}
}

func TestDeterministicIDStable(t *testing.T) {
	if DeterministicID("docs/EVAL.md") != DeterministicID("docs/EVAL.md") {
		t.Fatal("same key must yield same id")
	}
	if DeterministicID("a") == DeterministicID("b") {
		t.Fatal("different keys must differ")
	}
}

func TestLoadFiles(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "sub", "Doc.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("  真实内容  "), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, []byte("   "), 0o644); err != nil {
		t.Fatal(err)
	}

	user := DeterministicID("u")
	docs, err := LoadFiles([]string{full, empty}, LoadOptions{UserID: user, Root: dir})
	if err != nil {
		t.Fatalf("LoadFiles: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1 (空文件应跳过)", len(docs))
	}
	d := docs[0]
	if d.Key != filepath.Join("sub", "Doc.md") {
		t.Fatalf("key = %q", d.Key)
	}
	if d.Name != "Doc.md" {
		t.Fatalf("name = %q", d.Name)
	}
	if d.Content != "真实内容" {
		t.Fatalf("content = %q (应去首尾空白)", d.Content)
	}
	if d.Document.ID != DeterministicID(d.Key) {
		t.Fatal("document id must derive from key")
	}
	if d.Document.UserID != user || d.Document.FileExt != "md" || d.Document.SourceType != "file" {
		t.Fatalf("document meta = %+v", d.Document)
	}
}

func TestLoadFilesMissing(t *testing.T) {
	if _, err := LoadFiles([]string{filepath.Join(t.TempDir(), "nope.md")}, LoadOptions{}); err == nil {
		t.Fatal("missing file should error")
	}
}
