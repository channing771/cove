package search

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/boxify/api-go/internal/core/rag/vectorstore"
	"github.com/boxify/api-go/internal/core/valuex"
)

// fakeDense 实现 vectorstore.DenseIndex，记录 Search 入参并返回预置命中。
type fakeDense struct {
	calls      int
	lastVector []float64
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
	f.calls++
	f.lastVector = vector
	f.lastK = k
	f.lastFilter = filter
	if f.err != nil {
		return nil, f.err
	}
	return f.hits, nil
}

// fakeKeyword 实现 vectorstore.KeywordIndex，Search 返回 BM25 命中，Fetch 返回 parent 命中。
type fakeKeyword struct {
	searchCalls     int
	lastQuery       string
	lastK           int
	lastFilter      vectorstore.Filter
	hits            []vectorstore.Hit
	err             error
	fetchCalls      int
	lastFetchFilter vectorstore.Filter
	fetchHits       []vectorstore.Hit
	fetchErr        error
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
	f.searchCalls++
	f.lastQuery = query
	f.lastK = k
	f.lastFilter = filter
	if f.err != nil {
		return nil, f.err
	}
	return f.hits, nil
}
func (f *fakeKeyword) Fetch(ctx context.Context, filter vectorstore.Filter, size int) ([]vectorstore.Hit, error) {
	f.fetchCalls++
	f.lastFetchFilter = filter
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.fetchHits, nil
}

type fakeEmbedder struct {
	calls int
	vec   []float64
	err   error
	dim   int
}

func (e *fakeEmbedder) EmbedOne(ctx context.Context, text string, dimensions int) ([]float64, error) {
	e.calls++
	e.dim = dimensions
	if e.err != nil {
		return nil, e.err
	}
	return e.vec, nil
}

type fakeReranker struct {
	calls     int
	documents []string
	topN      int
	results   []RerankResult
	err       error
}

func (r *fakeReranker) Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error) {
	r.calls++
	r.documents = append([]string(nil), documents...)
	r.topN = topN
	if r.err != nil {
		return nil, r.err
	}
	return r.results, nil
}

type sourceMeta struct {
	DocName string
	KBID    string
}

func decodeSourceMeta(fields map[string]any) (sourceMeta, error) {
	return sourceMeta{
		DocName: valuex.String(fields["doc_name"]),
		KBID:    valuex.String(fields["kb_id"]),
	}, nil
}

// 验证 NewSearcher 使用默认配置，并且 WithOption 能覆盖默认值。
func TestNewSearcherAppliesOptions(t *testing.T) {
	reranker := &fakeReranker{}
	dense := &fakeDense{}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{}
	filterBuilder := func(ctx context.Context, req Input) (vectorstore.Filter, error) {
		return vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("tenant", "u-1")}}, nil
	}

	searcher := NewSearcher[sourceMeta](
		dense,
		keyword,
		WithEmbedder(embedder),
		WithEmbeddingDim(2048),
		WithRecallSize(30),
		WithVectorWeight(0.7),
		WithBM25Weight(0.3),
		WithReranker(reranker),
		WithRerankWindowSize(9),
		WithRerankTopK(4),
		WithRerankFailOpen(false),
		WithRerankMinScore(0.2),
		WithRerankDocumentBuilder(func(fields map[string]any) string {
			return "custom"
		}),
		WithLowRelevanceThreshold(0.45),
		WithFilterBuilder(filterBuilder),
		WithSourceDecoder[sourceMeta](decodeSourceMeta),
	)

	if searcher.dense != dense || searcher.keyword != keyword || searcher.Embedder != embedder {
		t.Fatalf("dependencies were not assigned")
	}
	if searcher.EmbeddingDim != 2048 {
		t.Fatalf("EmbeddingDim = %d, want 2048", searcher.EmbeddingDim)
	}
	if searcher.RecallSize != 30 {
		t.Fatalf("RecallSize = %d, want 30", searcher.RecallSize)
	}
	if searcher.VectorWeight != 0.7 || searcher.BM25Weight != 0.3 {
		t.Fatalf("weights = %v/%v, want 0.7/0.3", searcher.VectorWeight, searcher.BM25Weight)
	}
	if searcher.Reranker != reranker {
		t.Fatalf("Reranker = %#v, want fake reranker", searcher.Reranker)
	}
	if searcher.RerankWindowSize != 9 || searcher.RerankTopK != 4 || searcher.RerankFailOpen {
		t.Fatalf("rerank options = window %d topK %d open %v, want 9/4/false", searcher.RerankWindowSize, searcher.RerankTopK, searcher.RerankFailOpen)
	}
	if searcher.RerankMinScore == nil || *searcher.RerankMinScore != 0.2 || searcher.RerankDocumentBuilder == nil {
		t.Fatalf("rerank min/builder = %#v/%v, want configured", searcher.RerankMinScore, searcher.RerankDocumentBuilder != nil)
	}
	if searcher.LowRelevanceThreshold == nil || *searcher.LowRelevanceThreshold != 0.45 {
		t.Fatalf("LowRelevanceThreshold = %#v, want 0.45", searcher.LowRelevanceThreshold)
	}
	if searcher.FilterBuilder == nil || searcher.sourceDecoder == nil {
		t.Fatal("FilterBuilder or sourceDecoder is nil")
	}
}

// 验证分数归一化能处理空输入、同分输入和普通区间输入。
func TestNormalizeScores(t *testing.T) {
	if got := Normalize(nil); len(got) != 0 {
		t.Fatalf("Normalize(nil) = %#v, want empty", got)
	}
	if got := Normalize(map[string]float64{"a": 2, "b": 2}); !reflect.DeepEqual(got, map[string]float64{"a": 1, "b": 1}) {
		t.Fatalf("Normalize(equal) = %#v", got)
	}
	got := Normalize(map[string]float64{"low": 10, "mid": 15, "high": 20})
	want := map[string]float64{"low": 0, "mid": 0.5, "high": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize() = %#v, want %#v", got, want)
	}
}

// 验证 filter builder 出错时会直接返回错误，不访问 embedding 和存储。
func TestSearcherReturnsFilterBuilderErrorBeforeDependencies(t *testing.T) {
	wantErr := errors.New("build filter failed")
	dense := &fakeDense{}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	_, err := NewSearcher[sourceMeta](dense, keyword, WithEmbedder(embedder), WithFilterBuilder(func(ctx context.Context, req Input) (vectorstore.Filter, error) {
		return vectorstore.Filter{}, wantErr
	})).Search(context.Background(), "hello")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Search() error = %v, want %v", err, wantErr)
	}
	if embedder.calls != 0 || dense.calls != 0 || keyword.searchCalls != 0 {
		t.Fatalf("dependency calls = embedder %d dense %d keyword %d, want zero", embedder.calls, dense.calls, keyword.searchCalls)
	}
}

// 验证 Search 的 InputOption 会同时影响向量召回和 BM25 召回（中立过滤 + recallSize + query 透传）。
func TestSearcherUsesRequestOptionsInVectorAndKeywordRecall(t *testing.T) {
	filter := vectorstore.Filter{Must: []vectorstore.Condition{
		vectorstore.Eq("tenant", "u-1"),
		vectorstore.In("kb_id", []string{"kb-1", "kb-2"}),
	}}
	dense := &fakeDense{}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1, 0.2}}

	_, err := NewSearcher[sourceMeta](dense, keyword, WithEmbedder(embedder), WithEmbeddingDim(512)).
		Search(context.Background(), "search text", WithFilters(filter), WithTopK(3), WithInputRecallSize(7))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if embedder.calls != 1 || embedder.dim != 512 {
		t.Fatalf("embedder calls/dim = %d/%d, want 1/512", embedder.calls, embedder.dim)
	}
	if dense.calls != 1 || dense.lastK != 7 || !reflect.DeepEqual(dense.lastFilter, filter) {
		t.Fatalf("dense recall = calls %d k %d filter %#v, want 1/7/%#v", dense.calls, dense.lastK, dense.lastFilter, filter)
	}
	if keyword.searchCalls != 1 || keyword.lastK != 7 || keyword.lastQuery != "search text" || !reflect.DeepEqual(keyword.lastFilter, filter) {
		t.Fatalf("keyword recall = calls %d k %d query %q filter %#v", keyword.searchCalls, keyword.lastK, keyword.lastQuery, keyword.lastFilter)
	}
}

// 验证请求级 embedder 会覆盖构造级默认 embedder。
func TestSearcherUsesInputEmbedderBeforeDefaultEmbedder(t *testing.T) {
	dense := &fakeDense{}
	keyword := &fakeKeyword{}
	defaultEmbedder := &fakeEmbedder{vec: []float64{0.1}}
	inputEmbedder := &fakeEmbedder{vec: []float64{0.2}}

	_, err := NewSearcher[sourceMeta](dense, keyword, WithEmbedder(defaultEmbedder)).
		Search(context.Background(), "query", WithInputEmbedder(inputEmbedder))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if defaultEmbedder.calls != 0 {
		t.Fatalf("default embedder calls = %d, want 0", defaultEmbedder.calls)
	}
	if inputEmbedder.calls != 1 {
		t.Fatalf("input embedder calls = %d, want 1", inputEmbedder.calls)
	}
}

// 验证构造级和请求级都没有 embedder 时返回明确错误。
func TestSearcherReturnsErrorWithoutEmbedder(t *testing.T) {
	dense := &fakeDense{}
	keyword := &fakeKeyword{}

	_, err := NewSearcher[sourceMeta](dense, keyword).Search(context.Background(), "query")
	if err == nil || err.Error() != "rag search embedder is nil" {
		t.Fatalf("Search() error = %v, want rag search embedder is nil", err)
	}
	if dense.calls != 0 || keyword.searchCalls != 0 {
		t.Fatalf("store calls = dense %d keyword %d, want 0", dense.calls, keyword.searchCalls)
	}
}

// 验证自定义 filter builder 会收到内部 Input，并且 Input 未设置 recallSize 时使用 searcher option 默认值。
func TestSearcherUsesFilterBuilderAndOptionRecallSize(t *testing.T) {
	filter := vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("tenant", "from-builder")}}
	dense := &fakeDense{}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}
	calls := 0

	_, err := NewSearcher[sourceMeta](
		dense,
		keyword,
		WithEmbedder(embedder),
		WithRecallSize(11),
		WithFilterBuilder(func(ctx context.Context, req Input) (vectorstore.Filter, error) {
			calls++
			if req.Query != "query" || req.TopK != 2 {
				t.Fatalf("request passed to filter builder = %#v", req)
			}
			return filter, nil
		}),
	).Search(context.Background(), "query", WithTopK(2))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("filter builder calls = %d, want 1", calls)
	}
	if dense.lastK != 11 || !reflect.DeepEqual(dense.lastFilter, filter) {
		t.Fatalf("dense recall = k %d filter %#v, want 11/%#v", dense.lastK, dense.lastFilter, filter)
	}
	if keyword.lastK != 11 || !reflect.DeepEqual(keyword.lastFilter, filter) {
		t.Fatalf("keyword recall = k %d filter %#v, want 11/%#v", keyword.lastK, keyword.lastFilter, filter)
	}
}

// 验证结果只包含通用字段，业务元数据通过 decoder 放入 Source，parent 内容不影响 Source 来源。
func TestSearcherFusesScoresAndDecodesSource(t *testing.T) {
	childSrc := source("both child", "parent-both")
	childSrc["doc_name"] = "ChildDoc"
	dense := &fakeDense{hits: []vectorstore.Hit{
		hit("vec-only", 0.9, source("vec only", "")),
		hit("both", 0.8, childSrc),
	}}
	keyword := &fakeKeyword{
		hits: []vectorstore.Hit{
			hit("bm-only", 20, source("bm only", "")),
			hit("both", 10, childSrc),
		},
		fetchHits: []vectorstore.Hit{
			hit("parent-both-hit", 1, map[string]any{"chunk_id": "parent-both", "content": "parent content", "doc_name": "ParentDoc"}),
		},
	}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](dense, keyword, WithEmbedder(embedder), WithSourceDecoder[sourceMeta](decodeSourceMeta)).
		Search(context.Background(), "query", WithTopK(3))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	gotIDs := resultIDs(got)
	wantIDs := []string{"vec-only", "bm-only", "both"}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("result ids = %#v, want %#v; results=%#v", gotIDs, wantIDs, got)
	}
	if got.Results[0].Score != 0.6 || got.Results[1].Score != 0.4 || got.Results[2].Content != "parent content" {
		t.Fatalf("results = %#v, want fused scores and parent content", got)
	}
	if got.Results[2].Source.DocName != "ChildDoc" {
		t.Fatalf("decoded source = %#v, want child source metadata", got.Results[2].Source)
	}
}

// 验证 source decoder 出错时 Search 会返回错误，避免静默丢失业务元数据。
func TestSearcherReturnsDecoderError(t *testing.T) {
	wantErr := errors.New("decode failed")
	dense := &fakeDense{hits: []vectorstore.Hit{hit("a", 1, source("doc a", ""))}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	_, err := NewSearcher[sourceMeta](dense, keyword, WithEmbedder(embedder), WithSourceDecoder[sourceMeta](func(fields map[string]any) (sourceMeta, error) {
		return sourceMeta{}, wantErr
	})).Search(context.Background(), "query", WithTopK(1))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Search() error = %v, want %v", err, wantErr)
	}
}

// 验证未配置 source decoder 时，Output.Source 使用类型零值。
func TestSearcherUsesZeroSourceWithoutDecoder(t *testing.T) {
	dense := &fakeDense{hits: []vectorstore.Hit{hit("a", 1, source("doc a", ""))}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](dense, keyword, WithEmbedder(embedder)).Search(context.Background(), "query", WithTopK(1))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Source != (sourceMeta{}) {
		t.Fatalf("results = %#v, want zero source", got)
	}
}

// 验证 MinVectorScore 只过滤向量不达标的候选，保留候选继续参与 BM25 融合和 rerank。
// 约定 dense 分数即 cosine 相似度，直接与阈值比较。
func TestSearcherFiltersByMinVectorScore(t *testing.T) {
	threshold := 0.5
	reranker := &fakeReranker{results: []RerankResult{{Index: 0, Score: 1}, {Index: 1, Score: 0.9}}}
	dense := &fakeDense{hits: []vectorstore.Hit{
		hit("semantic", 0.8, source("semantic", "")),
		hit("lexical", 0.6, source("lexical", "")),
		hit("low", 0.4, source("low", "")),
	}}
	keyword := &fakeKeyword{hits: []vectorstore.Hit{
		hit("bm-only", 100, source("bm only", "")),
		hit("lexical", 20, source("lexical", "")),
		hit("semantic", 10, source("semantic", "")),
	}}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](
		dense,
		keyword,
		WithEmbedder(embedder),
		WithVectorWeight(0.4),
		WithBM25Weight(0.6),
		WithReranker(reranker),
	).Search(context.Background(), "query", WithTopK(5), WithMinVectorScore(threshold))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if gotIDs := resultIDs(got); !slices.Equal(gotIDs, []string{"lexical", "semantic"}) {
		t.Fatalf("result ids = %#v, want lexical,semantic", gotIDs)
	}
	if reranker.calls != 1 || !slices.Equal(reranker.documents, []string{"lexical", "semantic"}) {
		t.Fatalf("reranker calls/documents = %d/%#v", reranker.calls, reranker.documents)
	}
	if got.Results[0].Score != 0.6 || got.Results[1].Score != 0.4 {
		t.Fatalf("scores = %#v, want fused scores after vector gate", got)
	}
}

// 验证 child 指向的 parent 查不到时，会回退返回 child 自身内容。
func TestSearcherFallsBackToChildContentWhenParentMissing(t *testing.T) {
	dense := &fakeDense{hits: []vectorstore.Hit{hit("child", 1, source("child content", "missing-parent"))}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](dense, keyword, WithEmbedder(embedder)).Search(context.Background(), "query", WithTopK(1))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Content != "child content" {
		t.Fatalf("results = %#v, want child content fallback", got)
	}
	if keyword.fetchCalls != 1 {
		t.Fatalf("keyword fetch calls = %d, want 1", keyword.fetchCalls)
	}
}

// 验证 reranker 成功时按重排顺序返回，失败时回退融合排序。
func TestSearcherUsesRerankerAndFallsBackOnError(t *testing.T) {
	reranker := &fakeReranker{results: []RerankResult{{Index: 1, Score: 0.99}, {Index: 0, Score: 0.5}}}
	dense := &fakeDense{hits: []vectorstore.Hit{hit("a", 2, source("doc a", "")), hit("b", 1, source("doc b", ""))}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](dense, keyword, WithEmbedder(embedder), WithReranker(reranker)).Search(context.Background(), "query", WithTopK(2))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if gotIDs := resultIDs(got); !slices.Equal(gotIDs, []string{"b", "a"}) {
		t.Fatalf("reranked result ids = %#v, want b,a", gotIDs)
	}
	if got.Results[0].RerankScore == nil || *got.Results[0].RerankScore != 0.99 || got.Results[0].Score != 0 {
		t.Fatalf("rerank/fused score = %#v/%v, want 0.99/0", got.Results[0].RerankScore, got.Results[0].Score)
	}
	if reranker.calls != 1 || !slices.Equal(reranker.documents, []string{"doc a", "doc b"}) {
		t.Fatalf("reranker calls/documents = %d/%#v", reranker.calls, reranker.documents)
	}

	fallbackDense := &fakeDense{hits: []vectorstore.Hit{hit("a", 2, source("doc a", "")), hit("b", 1, source("doc b", ""))}}
	fallback, err := NewSearcher[sourceMeta](fallbackDense, &fakeKeyword{}, WithEmbedder(embedder), WithReranker(&fakeReranker{err: errors.New("rerank failed")})).
		Search(context.Background(), "query", WithTopK(2))
	if err != nil {
		t.Fatalf("fallback Search() error = %v", err)
	}
	if gotIDs := resultIDs(fallback); !slices.Equal(gotIDs, []string{"a", "b"}) {
		t.Fatalf("fallback result ids = %#v, want fused order a,b", gotIDs)
	}
}

// 验证低相关状态优先使用 rerank 分数，并且不会过滤原始结果。
func TestSearcherReportsLowRelevanceByRerankScore(t *testing.T) {
	reranker := &fakeReranker{results: []RerankResult{{Index: 0, Score: 0.3}, {Index: 1, Score: 0.2}}}
	dense := &fakeDense{hits: []vectorstore.Hit{hit("a", 0.95, source("doc a", "")), hit("b", 0.9, source("doc b", ""))}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](
		dense,
		keyword,
		WithEmbedder(embedder),
		WithReranker(reranker),
		WithLowRelevanceThreshold(0.5),
	).Search(context.Background(), "query", WithTopK(2))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got.Results) != 2 {
		t.Fatalf("Search().Results len = %d, want 2", len(got.Results))
	}
	if !got.Relevance.Low || got.Relevance.Basis != RelevanceBasisRerank {
		t.Fatalf("Search().Relevance = %#v, want low rerank status", got.Relevance)
	}
	if got.Relevance.MaxScore == nil || *got.Relevance.MaxScore != 0.3 {
		t.Fatalf("Search().Relevance.MaxScore = %#v, want 0.3", got.Relevance.MaxScore)
	}
	if got.Relevance.Threshold == nil || *got.Relevance.Threshold != 0.5 {
		t.Fatalf("Search().Relevance.Threshold = %#v, want 0.5", got.Relevance.Threshold)
	}
}

// 验证没有 rerank 分数时，低相关状态使用向量 cosine 分数（约定 dense 分数即 cosine）。
func TestSearcherReportsLowRelevanceByVectorCosine(t *testing.T) {
	dense := &fakeDense{hits: []vectorstore.Hit{hit("a", 0.4, source("doc a", "")), hit("b", 0.3, source("doc b", ""))}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](
		dense,
		keyword,
		WithEmbedder(embedder),
		WithLowRelevanceThreshold(0.5),
	).Search(context.Background(), "query", WithTopK(2))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if !got.Relevance.Low || got.Relevance.Basis != RelevanceBasisVector {
		t.Fatalf("Search().Relevance = %#v, want low vector status", got.Relevance)
	}
	if got.Relevance.MaxScore == nil || math.Abs(*got.Relevance.MaxScore-0.4) > 1e-9 {
		t.Fatalf("Search().Relevance.MaxScore = %#v, want cosine 0.4", got.Relevance.MaxScore)
	}
	if got.Results[0].Score != 0.6 {
		t.Fatalf("Search().Results[0].Score = %v, want weighted fused score 0.6", got.Results[0].Score)
	}
}

// 验证请求级低相关阈值会覆盖构造级阈值。
func TestSearcherInputLowRelevanceThresholdOverridesDefault(t *testing.T) {
	dense := &fakeDense{hits: []vectorstore.Hit{hit("a", 0.7, source("doc a", ""))}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](
		dense,
		keyword,
		WithEmbedder(embedder),
		WithLowRelevanceThreshold(0.9),
	).Search(context.Background(), "query", WithTopK(1), WithInputLowRelevanceThreshold(0.1))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if got.Relevance.Low {
		t.Fatalf("Search().Relevance.Low = true, want false after request threshold override")
	}
	if got.Relevance.Threshold == nil || *got.Relevance.Threshold != 0.1 {
		t.Fatalf("Search().Relevance.Threshold = %#v, want 0.1", got.Relevance.Threshold)
	}
}

// 验证请求级配置可以关闭构造级 reranker。
func TestSearcherCanDisableRerankPerInput(t *testing.T) {
	reranker := &fakeReranker{results: []RerankResult{{Index: 1, Score: 1}}}
	dense := &fakeDense{hits: []vectorstore.Hit{hit("a", 2, source("doc a", "")), hit("b", 1, source("doc b", ""))}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](dense, keyword, WithEmbedder(embedder), WithReranker(reranker)).
		Search(context.Background(), "query", WithTopK(2), WithInputRerankEnabled(false))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if reranker.calls != 0 {
		t.Fatalf("reranker calls = %d, want 0", reranker.calls)
	}
	if gotIDs := resultIDs(got); !slices.Equal(gotIDs, []string{"a", "b"}) {
		t.Fatalf("result ids = %#v, want fused order a,b", gotIDs)
	}
}

// 验证 rerank window 控制候选窗口，topK 透传给 reranker，文档 builder 覆盖默认 content。
func TestSearcherUsesRerankWindowTopKAndDocumentBuilder(t *testing.T) {
	reranker := &fakeReranker{results: []RerankResult{{Index: 1, Score: 0.8}, {Index: 0, Score: 0.7}}}
	first := source("doc a", "")
	first["title"] = "A"
	second := source("doc b", "")
	second["title"] = "B"
	third := source("doc c", "")
	third["title"] = "C"
	dense := &fakeDense{hits: []vectorstore.Hit{hit("a", 3, first), hit("b", 2, second), hit("c", 1, third)}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](
		dense,
		keyword,
		WithEmbedder(embedder),
		WithReranker(reranker),
		WithRerankWindowSize(2),
		WithRerankTopK(1),
		WithRerankDocumentBuilder(func(fields map[string]any) string {
			return valuex.String(fields["title"]) + ":" + valuex.String(fields["content"])
		}),
	).Search(context.Background(), "query", WithTopK(3))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if reranker.topN != 1 {
		t.Fatalf("reranker topN = %d, want 1", reranker.topN)
	}
	if !slices.Equal(reranker.documents, []string{"A:doc a", "B:doc b"}) {
		t.Fatalf("reranker documents = %#v, want first two built docs", reranker.documents)
	}
	if gotIDs := resultIDs(got); !slices.Equal(gotIDs, []string{"b", "a"}) {
		t.Fatalf("result ids = %#v, want b,a", gotIDs)
	}
}

// 验证 reranker 返回越界、重复和低分结果时会被过滤，并保留有效 rerank 分数。
func TestSearcherFiltersInvalidDuplicateAndLowScoreRerankResults(t *testing.T) {
	reranker := &fakeReranker{results: []RerankResult{
		{Index: 5, Score: 1},
		{Index: 1, Score: 0.4},
		{Index: 0, Score: 0.9},
		{Index: 0, Score: 0.8},
	}}
	dense := &fakeDense{hits: []vectorstore.Hit{hit("a", 2, source("doc a", "")), hit("b", 1, source("doc b", ""))}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](
		dense,
		keyword,
		WithEmbedder(embedder),
		WithReranker(reranker),
		WithRerankMinScore(0.5),
	).Search(context.Background(), "query", WithTopK(2))
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if gotIDs := resultIDs(got); !slices.Equal(gotIDs, []string{"a"}) {
		t.Fatalf("result ids = %#v, want only a", gotIDs)
	}
	if got.Results[0].RerankScore == nil || *got.Results[0].RerankScore != 0.9 {
		t.Fatalf("RerankScore = %#v, want 0.9", got.Results[0].RerankScore)
	}
}

// 验证 fail-open 关闭时，reranker 错误会直接返回。
func TestSearcherReturnsRerankErrorWhenFailClosed(t *testing.T) {
	wantErr := errors.New("rerank failed")
	dense := &fakeDense{hits: []vectorstore.Hit{hit("a", 1, source("doc a", ""))}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	_, err := NewSearcher[sourceMeta](
		dense,
		keyword,
		WithEmbedder(embedder),
		WithReranker(&fakeReranker{err: wantErr}),
		WithRerankFailOpen(false),
	).Search(context.Background(), "query", WithTopK(1))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Search() error = %v, want %v", err, wantErr)
	}
}

// 验证请求级 reranker、窗口、topK、失败策略、最低分和文档 builder 都优先于构造级配置。
func TestSearcherInputRerankOptionsOverrideSearcherOptions(t *testing.T) {
	defaultReranker := &fakeReranker{err: errors.New("should not be used")}
	inputReranker := &fakeReranker{results: []RerankResult{{Index: 1, Score: 0.6}, {Index: 0, Score: 0.4}}}
	dense := &fakeDense{hits: []vectorstore.Hit{hit("a", 3, source("doc a", "")), hit("b", 2, source("doc b", "")), hit("c", 1, source("doc c", ""))}}
	keyword := &fakeKeyword{}
	embedder := &fakeEmbedder{vec: []float64{0.1}}

	got, err := NewSearcher[sourceMeta](
		dense,
		keyword,
		WithEmbedder(embedder),
		WithReranker(defaultReranker),
		WithRerankWindowSize(3),
		WithRerankTopK(3),
		WithRerankFailOpen(false),
		WithRerankMinScore(0.1),
	).Search(
		context.Background(),
		"query",
		WithTopK(3),
		WithInputReranker(inputReranker),
		WithInputRerankWindowSize(2),
		WithInputRerankTopK(1),
		WithInputRerankFailOpen(true),
		WithInputRerankMinScore(0.5),
		WithInputRerankDocumentBuilder(func(fields map[string]any) string {
			return "input:" + valuex.String(fields["content"])
		}),
	)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if defaultReranker.calls != 0 || inputReranker.calls != 1 {
		t.Fatalf("reranker calls = default %d input %d, want 0/1", defaultReranker.calls, inputReranker.calls)
	}
	if inputReranker.topN != 1 || !slices.Equal(inputReranker.documents, []string{"input:doc a", "input:doc b"}) {
		t.Fatalf("input reranker topN/docs = %d/%#v, want 1/input docs", inputReranker.topN, inputReranker.documents)
	}
	if gotIDs := resultIDs(got); !slices.Equal(gotIDs, []string{"b"}) {
		t.Fatalf("result ids = %#v, want only b after min score", gotIDs)
	}
}

// 验证默认空检索器不返回结果，也不产生错误。
func TestNoopSearcherReturnsEmptyResult(t *testing.T) {
	got, err := NoopSearcher[sourceMeta]{}.Search(context.Background(), "query")
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}
	if got == nil || got.Results != nil {
		t.Fatalf("Search() = %#v, want nil", got)
	}
}

func hit(id string, score float64, fields map[string]any) vectorstore.Hit {
	return vectorstore.Hit{ID: id, Score: score, Fields: fields}
}

func source(content string, parentID string) map[string]any {
	src := map[string]any{
		"content":     content,
		"doc_name":    "Doc",
		"source_id":   "source-1",
		"source_type": "document",
		"kb_id":       "kb-1",
	}
	if parentID != "" {
		src["parent_id"] = parentID
	}
	return src
}

func resultIDs(result *SearchResult[sourceMeta]) []string {
	ids := make([]string, 0, len(result.Results))
	for _, result := range result.Results {
		ids = append(ids, result.ID)
	}
	return ids
}
