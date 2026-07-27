//go:build ragreal

// 真实数据 RAG 检索评测:把仓库自身的真实中文技术文档灌入真实 Qdrant + Elasticsearch,
// 经生产检索链路(ragsearch.Searcher 混合融合)跑自建数据集,产出报告与基线。
//
// 前置(本地起真实存储):
//
//	docker run -d --name cove-eval-qdrant -p 6333:6333 -p 6334:6334 qdrant/qdrant:latest
//	docker run -d --name cove-eval-es -p 9200:9200 -e discovery.type=single-node \
//	  -e xpack.security.enabled=false -e ES_JAVA_OPTS=-Xms1g\ -Xmx1g \
//	  docker.elastic.co/elasticsearch/elasticsearch:8.17.0
//
// 运行:
//
//	go test ./internal/eval/rag/ -tags ragreal -run TestRealDataRetrievalEval -v
//
// 可选 env:QDRANT_ADDR、ES_URL、EVAL_WRITE_BASELINE=1(把本次报告写为基线)。
package rag_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	ragsearch "github.com/boxify/api-go/internal/core/rag/search"
	"github.com/boxify/api-go/internal/core/rag/vectorstore"
	"github.com/boxify/api-go/internal/infrastructure/db/es"
	"github.com/boxify/api-go/internal/infrastructure/db/qdrant"
	"github.com/boxify/api-go/internal/models"
	"github.com/boxify/api-go/internal/repository/ragchunk"
	"github.com/boxify/api-go/internal/eval"
	"github.com/boxify/api-go/internal/eval/rag"
	"github.com/boxify/api-go/internal/eval/rag/corpus"
	"github.com/boxify/api-go/internal/eval/rag/ragadapter"
)

const (
	evalCollection = "cove_eval_chunks"
	embeddingDim   = 512
	// lowRelevanceThreshold 由实测校准:域内查询的最高向量分显著高于域外查询,
	// 取二者之间的分界。运行测试会打印每条用例的 max_score 供重新校准。
	lowRelevanceThreshold = 0.0
)

// corpusDir 是冻结的语料快照目录(仓库真实中文技术文档的副本)。
//
// 刻意不直接读活文档:语料里含 docs/EVAL.md,而记录本评测本身就会改它——活文档一变,
// chunk 与检索结果随之变化,基线便悄悄失效(实测 nDCG 0.9866→0.9933)。评测语料必须
// 冻结并随基线一起版本化;要更新语料时,同步刷新快照与基线。
const corpusDir = "testdata/corpus"

// corpusFiles 是语料文件名(相对 corpusDir),其 base name 即数据集里的 golden 标注。
var corpusFiles = []string{
	"EVAL.md",
	"OBSERVABILITY.md",
	"README.md",
	"2026-07-26-agent-harness-enterprise-design.md",
	"2026-07-26-llm-observability-otel-design.md",
	"2026-07-26-vector-store-abstraction-design.md",
	"2026-07-26-agent-eval-system-design.md",
}

// evalUser 固定评测语料归属,使检索过滤与生产一致(按 user_id 隔离)。
var evalUser = corpus.DeterministicID("eval-user")

// corpusPaths 返回冻结语料快照中各文件的路径。
func corpusPaths() []string {
	paths := make([]string, 0, len(corpusFiles))
	for _, f := range corpusFiles {
		paths = append(paths, filepath.Join(corpusDir, f))
	}
	return paths
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestRealDataRetrievalEval(t *testing.T) {
	ctx := context.Background()

	// --- 真实存储 ---
	dense, err := qdrant.NewDenseIndex(env("QDRANT_ADDR", "localhost:6334"), "", false, evalCollection)
	if err != nil {
		t.Fatalf("qdrant: %v (是否已起 cove-eval-qdrant?)", err)
	}
	esClient, err := es.NewClient(es.Config{URL: env("ES_URL", "http://localhost:9200")})
	if err != nil {
		t.Fatalf("es client: %v (是否已起 cove-eval-es?)", err)
	}
	keyword := es.NewKeywordIndex(esClient, evalCollection)

	// 先清空存储再灌:ES 的 BM25 IDF 统计会把"已删除但未段合并"的文档计入,
	// 反复 delete+reindex 会让词项统计逐轮漂移,进而改变融合排序。只有从干净索引
	// 开始,评测结果才可复现、阈值门禁才不抖动。
	resetStores(t)

	// --- 真实语料 → 生产分块 → 嵌入 → 生产双写 ---
	docs, err := corpus.LoadFiles(corpusPaths(), corpus.LoadOptions{UserID: evalUser, Root: corpusDir})
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(docs) != len(corpusFiles) {
		t.Fatalf("loaded %d docs, want %d", len(docs), len(corpusFiles))
	}
	embedder := corpus.NewHashEmbedder(embeddingDim)
	ingester := &corpus.Ingester{Dense: dense, Keyword: keyword, Embedder: embedder, Dim: embeddingDim}
	chunks, err := ingester.Ingest(ctx, docs)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	t.Logf("已把 %d 篇真实文档灌入 Qdrant+ES,共 %d 个 chunk", len(docs), chunks)
	waitSearchable(ctx, t, keyword)

	// --- 真实检索链路 ---
	repo := ragchunk.NewRepository(dense, keyword)
	searcher := ragsearch.NewSearcher[models.RAGChunkSource](dense, keyword,
		ragsearch.WithEmbeddingDim(embeddingDim),
		ragsearch.WithEmbedder(embedder),
		// 低相关阈值:按实测的向量分分布校准(见 lowRelevanceThreshold 注释),
		// 使域外问题被判为低相关,域内问题不被误判。
		ragsearch.WithLowRelevanceThreshold(lowRelevanceThreshold),
		ragsearch.WithSourceDecoder[models.RAGChunkSource](repo.DecodeSource))
	retriever := ragadapter.SearcherRetriever{
		Searcher: searcher,
		Embedder: embedder,
		Filter:   vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("user_id", evalUser.String())}},
	}

	// --- 自建数据集 → 评测 ---
	ds, err := eval.LoadDataset("testdata/datasets/cove-docs.json")
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	// 先逐条打印真实命中与低相关判定,便于人工核对 golden 标注与阈值校准(而非盲信指标)。
	for _, c := range ds.Cases {
		hits, relevance, err := retriever.RetrieveWithRelevance(ctx, c.Query, 5)
		if err != nil {
			t.Fatalf("retrieve %s: %v", c.ID, err)
		}
		names := make([]string, 0, len(hits))
		seen := map[string]bool{}
		for _, h := range hits {
			if !seen[h.DocName] {
				seen[h.DocName] = true
				names = append(names, h.DocName)
			}
		}
		maxScore := "n/a"
		if relevance.MaxScore != nil {
			maxScore = fmt.Sprintf("%.4f", *relevance.MaxScore)
		}
		t.Logf("[%s] %q\n    golden=%v\n    命中文档(按序)=%v\n    低相关=%v basis=%s max_score=%s",
			c.ID, c.Query, c.Expect["relevant_ids"], names, relevance.Low, relevance.Basis, maxScore)
	}

	e := &rag.Evaluator{
		Runner: &rag.RetrievalRunner{Retriever: retriever, TopK: 5},
		Scorers: []rag.Scorer{
			rag.RecallAtK(), rag.PrecisionAtK(), rag.HitRate(),
			rag.MRR(), rag.NDCG(), rag.MAP(), rag.F1AtK(),
			rag.LowRelevanceIs(),
		},
	}
	rep, err := e.Run(ctx, ds)
	if err != nil {
		t.Fatalf("eval run: %v", err)
	}

	_ = rep.WriteTable(logWriter{t})
	for name, agg := range rep.Scorers {
		t.Logf("scorer %-16s mean=%.4f pass=%d fail=%d skip=%d", name, agg.MeanValue, agg.Passed, agg.Failed, agg.Skipped)
	}
	t.Logf("pass_rate = %.2f%%", rep.PassRate*100)

	reportPath := filepath.Join("testdata", "reports", "cove-docs.json")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatalf("mkdir reports: %v", err)
	}
	writeJSON(t, reportPath, rep)

	baselinePath := filepath.Join("testdata", "baselines", "cove-docs.json")
	if os.Getenv("EVAL_WRITE_BASELINE") == "1" {
		writeJSON(t, baselinePath, rep)
		t.Logf("已写入基线 %s", baselinePath)
		return
	}
	baseline, err := eval.LoadReport(baselinePath)
	if err != nil {
		t.Fatalf("load baseline: %v(首次请用 EVAL_WRITE_BASELINE=1 生成)", err)
	}
	if err := rep.Diff(baseline).Err(); err != nil {
		t.Error(err)
	}
}

// TestRealDataWeightComparison 用同一真实语料与数据集横向对比不同融合权重,
// 演示 RAG 调参的主用途:量化"向量权重调高/调低"对检索质量的实际影响。
//
//	go test ./internal/eval/rag/ -tags ragreal -run TestRealDataWeightComparison -v
func TestRealDataWeightComparison(t *testing.T) {
	ctx := context.Background()
	dense, keyword, embedder := setupRealStores(ctx, t)
	repo := ragchunk.NewRepository(dense, keyword)
	filter := vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("user_id", evalUser.String())}}

	// 每个变体是一套独立的融合权重(权重属构造级配置)。
	newVariant := func(name string, vectorWeight, bm25Weight float64) rag.Variant {
		searcher := ragsearch.NewSearcher[models.RAGChunkSource](dense, keyword,
			ragsearch.WithEmbeddingDim(embeddingDim),
			ragsearch.WithEmbedder(embedder),
			ragsearch.WithVectorWeight(vectorWeight),
			ragsearch.WithBM25Weight(bm25Weight),
			ragsearch.WithSourceDecoder[models.RAGChunkSource](repo.DecodeSource))
		return rag.Variant{
			Name: name,
			Runner: &rag.RetrievalRunner{
				Retriever: ragadapter.SearcherRetriever{Searcher: searcher, Embedder: embedder, Filter: filter},
				TopK:      5,
			},
		}
	}

	ds, err := eval.LoadDataset("testdata/datasets/cove-docs.json")
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	cmp, err := (&rag.Comparer{
		Variants: []rag.Variant{
			newVariant("balanced-0.6/0.4", 0.6, 0.4),
			newVariant("vector-heavy-0.9/0.1", 0.9, 0.1),
			newVariant("bm25-heavy-0.1/0.9", 0.1, 0.9),
		},
		Scorers: []rag.Scorer{
			rag.RecallAtK(), rag.PrecisionAtK(), rag.MRR(), rag.NDCG(), rag.MAP(), rag.F1AtK(),
		},
	}).Run(ctx, ds)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}

	_ = cmp.WriteTable(logWriter{t})
	for _, scorer := range []string{"ndcg", "map", "mrr", "f1_at_k"} {
		t.Logf("最佳配置 by %-8s = %s", scorer, cmp.Best(scorer))
	}
	for _, d := range cmp.Delta("balanced-0.6/0.4", "bm25-heavy-0.1/0.9") {
		t.Logf("bm25-heavy 相对 balanced:%-14s %+.4f (%.4f → %.4f)", d.Scorer, d.Delta, d.Base, d.Candidate)
	}

	if len(cmp.Variants) != 3 {
		t.Fatalf("variants = %d, want 3", len(cmp.Variants))
	}
	for _, v := range cmp.Variants {
		if v.Report == nil || len(v.Report.Cases) != len(ds.Cases) {
			t.Fatalf("变体 %q 报告不完整", v.Name)
		}
	}
}

// setupRealStores 复位并灌入真实语料,返回可用于检索的存储与嵌入器。
func setupRealStores(ctx context.Context, t *testing.T) (*qdrant.DenseIndex, *es.KeywordIndex, *corpus.HashEmbedder) {
	t.Helper()
	dense, err := qdrant.NewDenseIndex(env("QDRANT_ADDR", "localhost:6334"), "", false, evalCollection)
	if err != nil {
		t.Fatalf("qdrant: %v", err)
	}
	esClient, err := es.NewClient(es.Config{URL: env("ES_URL", "http://localhost:9200")})
	if err != nil {
		t.Fatalf("es client: %v", err)
	}
	keyword := es.NewKeywordIndex(esClient, evalCollection)
	resetStores(t)

	docs, err := corpus.LoadFiles(corpusPaths(), corpus.LoadOptions{UserID: evalUser, Root: corpusDir})
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	embedder := corpus.NewHashEmbedder(embeddingDim)
	if _, err := (&corpus.Ingester{Dense: dense, Keyword: keyword, Embedder: embedder, Dim: embeddingDim}).Ingest(ctx, docs); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	waitSearchable(ctx, t, keyword)
	return dense, keyword, embedder
}

// resetStores 删除评测专用的 ES 索引与 Qdrant collection,使每次评测从干净状态开始。
//
// 二者随后会由 Ingester 的 EnsureIndex/EnsureCollection 重建。删除不存在的索引返回 404,
// 属正常情形,不视为失败。
func resetStores(t *testing.T) {
	t.Helper()
	del := func(url string) {
		req, err := http.NewRequest(http.MethodDelete, url, nil)
		if err != nil {
			t.Fatalf("build delete request %s: %v", url, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("delete %s: %v", url, err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode >= 500 {
			t.Fatalf("delete %s: status %d", url, resp.StatusCode)
		}
	}
	del(env("ES_URL", "http://localhost:9200") + "/" + evalCollection)
	del(env("QDRANT_HTTP", "http://localhost:6333") + "/collections/" + evalCollection)
}

// waitSearchable 等 ES 刷新可见(近实时索引),避免刚写入就检索不到。
func waitSearchable(ctx context.Context, t *testing.T, keyword *es.KeywordIndex) {
	t.Helper()
	filter := vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("user_id", evalUser.String())}}
	for i := 0; i < 30; i++ {
		hits, err := keyword.Fetch(ctx, filter, 1)
		if err == nil && len(hits) > 0 {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("等待 ES 可检索超时")
}

func writeJSON(t *testing.T, path string, rep *eval.Report) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := rep.WriteJSON(f); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

type logWriter struct{ t *testing.T }

func (w logWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }
