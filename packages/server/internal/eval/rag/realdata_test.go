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
)

// corpusFiles 是语料:仓库自身的真实中文技术文档(相对 packages/server)。
var corpusFiles = []string{
	"docs/EVAL.md",
	"deployments/OBSERVABILITY.md",
	"internal/core/agent/README.md",
	"docs/superpowers/specs/2026-07-26-agent-harness-enterprise-design.md",
	"docs/superpowers/specs/2026-07-26-llm-observability-otel-design.md",
	"docs/superpowers/specs/2026-07-26-vector-store-abstraction-design.md",
	"docs/superpowers/specs/2026-07-26-agent-eval-system-design.md",
}

// evalUser 固定评测语料归属,使检索过滤与生产一致(按 user_id 隔离)。
var evalUser = corpus.DeterministicID("eval-user")

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestRealDataRetrievalEval(t *testing.T) {
	ctx := context.Background()
	serverRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve server root: %v", err)
	}

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
	paths := make([]string, 0, len(corpusFiles))
	for _, f := range corpusFiles {
		paths = append(paths, filepath.Join(serverRoot, f))
	}
	docs, err := corpus.LoadFiles(paths, corpus.LoadOptions{UserID: evalUser, Root: serverRoot})
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
	// 先逐条打印真实命中,便于人工核对 golden 标注是否站得住(而非盲信指标)。
	for _, c := range ds.Cases {
		hits, err := retriever.Retrieve(ctx, c.Query, 5)
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
		t.Logf("[%s] %q\n    golden=%v\n    命中文档(按序)=%v", c.ID, c.Query, c.Expect["relevant_ids"], names)
	}

	e := &rag.Evaluator{
		Runner:  &rag.RetrievalRunner{Retriever: retriever, TopK: 5},
		Scorers: []rag.Scorer{rag.RecallAtK(), rag.PrecisionAtK(), rag.HitRate(), rag.MRR(), rag.NDCG()},
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
