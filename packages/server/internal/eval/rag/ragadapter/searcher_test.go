package ragadapter

import (
	"context"
	"errors"
	"testing"

	ragsearch "github.com/boxify/api-go/internal/core/rag/search"
	"github.com/boxify/api-go/internal/core/rag/vectorstore"
	"github.com/boxify/api-go/internal/models"
	"github.com/google/uuid"
)

// fakeDense 实现 vectorstore.DenseIndex，记录 Search 入参并返回预置命中。
type fakeDense struct {
	lastK      int
	lastFilter vectorstore.Filter
	hits       []vectorstore.Hit
	err        error
}

func (f *fakeDense) EnsureCollection(ctx context.Context, dim int) error          { return nil }
func (f *fakeDense) Upsert(ctx context.Context, points []vectorstore.Point) error { return nil }
func (f *fakeDense) DeleteByFilter(ctx context.Context, filter vectorstore.Filter) error {
	return nil
}
func (f *fakeDense) SetFields(ctx context.Context, filter vectorstore.Filter, fields map[string]any) error {
	return nil
}
func (f *fakeDense) Search(ctx context.Context, vector []float64, k int, filter vectorstore.Filter) ([]vectorstore.Hit, error) {
	f.lastK = k
	f.lastFilter = filter
	if f.err != nil {
		return nil, f.err
	}
	return f.hits, nil
}

// fakeKeyword 实现 vectorstore.KeywordIndex，本用例只走 dense 通道，关键词命中为空。
type fakeKeyword struct {
	lastFilter vectorstore.Filter
	hits       []vectorstore.Hit
}

func (f *fakeKeyword) EnsureIndex(ctx context.Context) error                     { return nil }
func (f *fakeKeyword) Index(ctx context.Context, docs []vectorstore.Point) error { return nil }
func (f *fakeKeyword) DeleteByFilter(ctx context.Context, filter vectorstore.Filter) error {
	return nil
}
func (f *fakeKeyword) SetFields(ctx context.Context, filter vectorstore.Filter, fields map[string]any) error {
	return nil
}
func (f *fakeKeyword) Search(ctx context.Context, query string, k int, filter vectorstore.Filter) ([]vectorstore.Hit, error) {
	f.lastFilter = filter
	return f.hits, nil
}
func (f *fakeKeyword) Fetch(ctx context.Context, filter vectorstore.Filter, size int) ([]vectorstore.Hit, error) {
	return nil, nil
}

type fakeEmbedder struct{ calls int }

func (e *fakeEmbedder) EmbedOne(ctx context.Context, text string, dimensions int) ([]float64, error) {
	e.calls++
	return []float64{0.1, 0.2}, nil
}

type fakeReranker struct{ results []ragsearch.RerankResult }

func (r *fakeReranker) Rerank(ctx context.Context, query string, documents []string, topN int) ([]ragsearch.RerankResult, error) {
	return r.results, nil
}

// decodeSource 模拟生产 ragchunk.DecodeSource:从命中字段还原文档身份。
func decodeSource(fields map[string]any) (models.RAGChunkSource, error) {
	src := models.RAGChunkSource{Name: str(fields["name"]), SourceType: str(fields["source_type"])}
	if id, err := uuid.Parse(str(fields["source_id"])); err == nil {
		src.SourceID = id
	}
	if id, err := uuid.Parse(str(fields["chunk_id"])); err == nil {
		src.ChunkID = id
	}
	return src, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

var (
	docA = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	docB = uuid.MustParse("22222222-2222-2222-2222-222222222222")
)

func hit(id string, score float64, sourceID uuid.UUID, name, content string) vectorstore.Hit {
	return vectorstore.Hit{
		ID:    id,
		Score: score,
		Fields: map[string]any{
			"chunk_id":  id,
			"source_id": sourceID.String(),
			"name":      name,
			"content":   content,
		},
	}
}

func newSearcher(dense vectorstore.DenseIndex, keyword vectorstore.KeywordIndex, opts ...ragsearch.Option) *ragsearch.Searcher[models.RAGChunkSource] {
	base := []ragsearch.Option{ragsearch.WithSourceDecoder[models.RAGChunkSource](decodeSource)}
	return ragsearch.NewSearcher[models.RAGChunkSource](dense, keyword, append(base, opts...)...)
}

// 验证 Output → RetrievedHit 的字段映射:chunk id、文档身份/名称、内容、分数。
func TestSearcherRetrieverMapsFields(t *testing.T) {
	dense := &fakeDense{hits: []vectorstore.Hit{
		hit("c1", 0.9, docA, "向量库指南", "向量数据库存的是向量"),
		hit("c2", 0.5, docB, "重排序说明", "rerank 是二阶段"),
	}}
	emb := &fakeEmbedder{}
	r := SearcherRetriever{Searcher: newSearcher(dense, &fakeKeyword{}), Embedder: emb}

	hits, err := r.Retrieve(context.Background(), "什么是向量数据库", 5)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	if emb.calls != 1 {
		t.Fatalf("embedder calls = %d, want 1 (embedder must be forwarded)", emb.calls)
	}
	top := hits[0]
	if top.ChunkID != "c1" {
		t.Errorf("ChunkID = %q, want c1", top.ChunkID)
	}
	if top.DocID != docA.String() {
		t.Errorf("DocID = %q, want %s (Source.SourceID)", top.DocID, docA)
	}
	if top.DocName != "向量库指南" {
		t.Errorf("DocName = %q", top.DocName)
	}
	if top.Content != "向量数据库存的是向量" {
		t.Errorf("Content = %q", top.Content)
	}
	if top.Score <= 0 {
		t.Errorf("Score = %v, want > 0", top.Score)
	}
	if top.RerankScore != nil {
		t.Errorf("RerankScore = %v, want nil without reranker", *top.RerankScore)
	}
	if hits[1].DocID != docB.String() {
		t.Errorf("second DocID = %q, want %s", hits[1].DocID, docB)
	}
}

// 验证 topK 截断与 Filter 透传到两个端口。
func TestSearcherRetrieverPassesTopKAndFilter(t *testing.T) {
	dense := &fakeDense{hits: []vectorstore.Hit{
		hit("c1", 0.9, docA, "a", "aa"),
		hit("c2", 0.7, docB, "b", "bb"),
		hit("c3", 0.5, docA, "c", "cc"),
	}}
	keyword := &fakeKeyword{}
	filter := vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("user_id", "u1")}}
	r := SearcherRetriever{Searcher: newSearcher(dense, keyword), Embedder: &fakeEmbedder{}, Filter: filter}

	hits, err := r.Retrieve(context.Background(), "q", 2)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2 (topK truncation)", len(hits))
	}
	for _, got := range []vectorstore.Filter{dense.lastFilter, keyword.lastFilter} {
		if len(got.Must) != 1 || got.Must[0].Field != "user_id" || got.Must[0].Value != "u1" {
			t.Fatalf("filter not forwarded: %+v", got)
		}
	}
}

// 验证 rerank 分数被映射出来(生产可插拔的二阶段)。
func TestSearcherRetrieverMapsRerankScore(t *testing.T) {
	dense := &fakeDense{hits: []vectorstore.Hit{
		hit("c1", 0.4, docA, "a", "aa"),
		hit("c2", 0.9, docB, "b", "bb"),
	}}
	// reranker 把第 2 个候选提到最前。
	rr := &fakeReranker{results: []ragsearch.RerankResult{{Index: 1, Score: 0.95}, {Index: 0, Score: 0.10}}}
	r := SearcherRetriever{
		Searcher: newSearcher(dense, &fakeKeyword{}, ragsearch.WithReranker(rr)),
		Embedder: &fakeEmbedder{},
	}
	hits, err := r.Retrieve(context.Background(), "q", 2)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].RerankScore == nil {
		t.Fatal("RerankScore not mapped")
	}
	if *hits[0].RerankScore != 0.95 {
		t.Fatalf("RerankScore = %v, want 0.95", *hits[0].RerankScore)
	}
}

// 验证检索错误向上传播(不吞错)。
func TestSearcherRetrieverPropagatesError(t *testing.T) {
	dense := &fakeDense{err: errors.New("dense down")}
	r := SearcherRetriever{Searcher: newSearcher(dense, &fakeKeyword{}), Embedder: &fakeEmbedder{}}
	if _, err := r.Retrieve(context.Background(), "q", 3); err == nil {
		t.Fatal("want error propagated from dense index")
	}
}
