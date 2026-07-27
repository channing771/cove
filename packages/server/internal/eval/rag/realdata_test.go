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
	"strconv"
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
	// lowRelevanceThreshold 由实测校准:域内查询的最高向量分应显著高于域外查询,
	// 取二者之间的分界。运行测试会打印每条用例的 max_score 供重新校准。
	// 词形 HashEmbedder 下两者区间重叠、无可分阈值(取 0 即不触发);GLM 语义向量下
	// 由 glmLowRelevanceThreshold 生效。
	lowRelevanceThreshold    = 0.0
	// GLM 实测:域内 [0.5957, 0.7852]、域外 [0.3614, 0.3897],取中间值留足两侧余量。
	glmLowRelevanceThreshold = 0.49
)

// embedderSetup 描述本次评测使用的向量模型及其配套的隔离参数。
//
// 不同嵌入模型的向量维度与语义空间都不同,必须各自使用独立的 collection/index 与基线,
// 否则会互相污染(维度不匹配直接写入失败,或用旧向量算出无意义的指标)。
type embedderSetup struct {
	name       string
	embedder   corpus.BatchEmbedderQuerier
	dim        int
	collection string
	baseline   string
	thresholds string
	lowThresh  float64
}

// resolveEmbedder 按环境变量选择向量模型:
//
//	GLM_API_KEY(或 ZHIPU_API_KEY)存在 → GLM embedding-3(真实语义向量)
//	否则                              → 确定性 HashEmbedder(词形,离线可复现)
//
// 可选 env:GLM_EMBEDDING_MODEL、GLM_BASE_URL、GLM_EMBEDDING_DIM。
func resolveEmbedder(t *testing.T) embedderSetup {
	t.Helper()
	apiKey := env("GLM_API_KEY", os.Getenv("ZHIPU_API_KEY"))
	if apiKey == "" {
		return embedderSetup{
			name:       "hash(词形,离线)",
			embedder:   corpus.NewHashEmbedder(512),
			dim:        512,
			collection: "cove_eval_chunks",
			baseline:   filepath.Join("testdata", "baselines", "cove-docs.json"),
			thresholds: filepath.Join("testdata", "thresholds", "hash.json"),
			lowThresh:  lowRelevanceThreshold,
		}
	}
	dim := 1024
	if v := os.Getenv("GLM_EMBEDDING_DIM"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed <= 0 {
			t.Fatalf("GLM_EMBEDDING_DIM 非法: %q", v)
		}
		dim = parsed
	}
	model := env("GLM_EMBEDDING_MODEL", corpus.GLMEmbeddingModel)
	embedder, err := corpus.NewGLMEmbedder(apiKey, model, os.Getenv("GLM_BASE_URL"), dim)
	if err != nil {
		t.Fatalf("构造 GLM 嵌入器失败: %v", err)
	}
	return embedderSetup{
		name:       fmt.Sprintf("GLM %s(语义,dim=%d)", model, dim),
		embedder:   embedder,
		dim:        dim,
		collection: fmt.Sprintf("cove_eval_chunks_glm_%d", dim),
		baseline:   filepath.Join("testdata", "baselines", "cove-docs-glm.json"),
		thresholds: filepath.Join("testdata", "thresholds", "glm.json"),
		lowThresh:  glmLowRelevanceThreshold,
	}
}

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

// loadDatasetWithThresholds 载入数据集并合入该向量模型对应的阈值剖面。
//
// 数据集只存 golden 等客观标注;阈值是"对某一配置的期望",按 setup 选择剖面,
// 使词形与语义两套配置各有独立门禁、互不牵连。
func loadDatasetWithThresholds(t *testing.T, path string, setup embedderSetup) (*eval.Dataset, error) {
	t.Helper()
	ds, err := eval.LoadDataset(path)
	if err != nil {
		return nil, err
	}
	profile, err := eval.LoadThresholds(setup.thresholds)
	if err != nil {
		return nil, err
	}
	if err := profile.ApplyTo(ds); err != nil {
		return nil, err
	}
	return ds, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestRealDataRetrievalEval(t *testing.T) {
	ctx := context.Background()
	setup := resolveEmbedder(t)
	t.Logf("向量模型: %s | collection=%s | 基线=%s", setup.name, setup.collection, setup.baseline)

	dense, keyword := openStores(t, setup)
	resetStores(t, setup)
	ingestCorpus(ctx, t, setup, dense, keyword)

	// --- 真实检索链路 ---
	repo := ragchunk.NewRepository(dense, keyword)
	searcher := ragsearch.NewSearcher[models.RAGChunkSource](dense, keyword,
		ragsearch.WithEmbeddingDim(setup.dim),
		ragsearch.WithEmbedder(setup.embedder),
		// 低相关阈值:按实测的向量分分布校准,使域外问题被判为低相关、域内不被误判。
		ragsearch.WithLowRelevanceThreshold(setup.lowThresh),
		ragsearch.WithSourceDecoder[models.RAGChunkSource](repo.DecodeSource))
	retriever := ragadapter.SearcherRetriever{
		Searcher: searcher,
		Embedder: setup.embedder,
		Filter:   vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("user_id", evalUser.String())}},
	}

	// --- 自建数据集 → 评测 ---
	ds, err := loadDatasetWithThresholds(t, "testdata/datasets/cove-docs.json", setup)
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

	reportPath := filepath.Join("testdata", "reports", filepath.Base(setup.baseline))
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatalf("mkdir reports: %v", err)
	}
	writeJSON(t, reportPath, rep)

	baselinePath := setup.baseline
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

// otherUser 是不拥有任何评测语料的租户,用于越权检索验证。
var otherUser = corpus.DeterministicID("eval-user-other")

// TestRealDataTenantIsolationGate 验证租户隔离:换一个 user_id 检索,必须一条都查不到。
//
// 这是**安全属性**而非质量指标——生产的 knowledge_search 始终按 user_id/kb_id 过滤,
// 过滤一旦失效就是跨租户数据泄露。用例复用域内那批"确定能检索到内容"的查询:
//   - 对照组(正确 user_id):必须有命中,证明语料确实在库、查询确实可召回;
//   - 越权组(其他 user_id):必须零命中。
//
// 两组同时断言,才能区分"隔离生效"与"检索整体挂了"——只测越权组的话,检索全挂也会绿。
//
//	go test ./internal/eval/rag/ -tags ragreal -run TestRealDataTenantIsolationGate -v
func TestRealDataTenantIsolationGate(t *testing.T) {
	ctx := context.Background()
	dense, keyword, setup := setupRealStores(ctx, t)
	t.Logf("向量模型: %s", setup.name)

	repo := ragchunk.NewRepository(dense, keyword)
	searcher := ragsearch.NewSearcher[models.RAGChunkSource](dense, keyword,
		ragsearch.WithEmbeddingDim(setup.dim),
		ragsearch.WithEmbedder(setup.embedder),
		ragsearch.WithSourceDecoder[models.RAGChunkSource](repo.DecodeSource))

	retrieverFor := func(userID string) ragadapter.SearcherRetriever {
		return ragadapter.SearcherRetriever{
			Searcher: searcher,
			Embedder: setup.embedder,
			Filter:   vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("user_id", userID)}},
		}
	}

	ds, err := eval.LoadDataset("testdata/datasets/cove-docs.json")
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}

	// 对照组:正确租户必须有命中(否则说明是检索坏了,而不是隔离生效)。
	control := &eval.Dataset{Name: "tenant-control", Cases: make([]eval.Case, 0, len(ds.Cases))}
	// 越权组:其他租户必须零命中。
	breach := &eval.Dataset{Name: "tenant-breach", Cases: make([]eval.Case, 0, len(ds.Cases))}
	for _, c := range ds.Cases {
		control.Cases = append(control.Cases, eval.Case{
			ID: c.ID + "/own", Query: c.Query, Tags: []string{"tenant-control"},
			Expect: map[string]any{"expect_no_results": false, "k": 5},
		})
		breach.Cases = append(breach.Cases, eval.Case{
			ID: c.ID + "/cross-tenant", Query: c.Query, Tags: []string{"tenant-breach"},
			Expect: map[string]any{"expect_no_results": true, "k": 5},
		})
	}

	run := func(ds *eval.Dataset, userID string) *eval.Report {
		e := &rag.Evaluator{
			Runner:  &rag.RetrievalRunner{Retriever: retrieverFor(userID), TopK: 5},
			Scorers: []rag.Scorer{rag.NoResults()},
		}
		rep, err := e.Run(ctx, ds)
		if err != nil {
			t.Fatalf("eval run: %v", err)
		}
		return rep
	}

	controlRep := run(control, evalUser.String())
	breachRep := run(breach, otherUser.String())

	t.Logf("对照组(自有租户,应有命中): pass_rate=%.2f%%", controlRep.PassRate*100)
	t.Logf("越权组(其他租户,应零命中): pass_rate=%.2f%%", breachRep.PassRate*100)
	if controlRep.PassRate != 1 {
		t.Fatalf("对照组 pass_rate=%.2f%%,说明检索本身有问题,越权组结论不可信", controlRep.PassRate*100)
	}
	if breachRep.PassRate != 1 {
		_ = breachRep.WriteTable(logWriter{t})
		t.Errorf("越权检索泄露:pass_rate=%.2f%%,应为 100%%", breachRep.PassRate*100)
	}
}

// TestRealDataNegativeGate 用负例数据集验证:知识库外的问题必须被判为低相关。
//
// 仅在使用真实语义向量(GLM)时有意义并执行——词形 HashEmbedder 下域内/域外分数区间
// 重叠,不存在无误判的阈值,故自动跳过而非给出假绿。
//
//	GLM_API_KEY=... go test ./internal/eval/rag/ -tags ragreal -run TestRealDataNegativeGate -v
func TestRealDataNegativeGate(t *testing.T) {
	ctx := context.Background()
	dense, keyword, setup := setupRealStores(ctx, t)
	if setup.lowThresh <= 0 {
		t.Skipf("当前向量模型(%s)无可分的低相关阈值,跳过负例门禁", setup.name)
	}
	t.Logf("向量模型: %s | 低相关阈值=%.2f", setup.name, setup.lowThresh)

	repo := ragchunk.NewRepository(dense, keyword)
	searcher := ragsearch.NewSearcher[models.RAGChunkSource](dense, keyword,
		ragsearch.WithEmbeddingDim(setup.dim),
		ragsearch.WithEmbedder(setup.embedder),
		ragsearch.WithLowRelevanceThreshold(setup.lowThresh),
		ragsearch.WithSourceDecoder[models.RAGChunkSource](repo.DecodeSource))
	retriever := ragadapter.SearcherRetriever{
		Searcher: searcher,
		Embedder: setup.embedder,
		Filter:   vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("user_id", evalUser.String())}},
	}

	negatives, err := eval.LoadDataset("testdata/datasets/cove-docs-negatives.json")
	if err != nil {
		t.Fatalf("load negatives: %v", err)
	}
	// 域内用例同时参与:确保阈值不会把正常问题误判为低相关(假阳性)。
	positives, err := eval.LoadDataset("testdata/datasets/cove-docs.json")
	if err != nil {
		t.Fatalf("load positives: %v", err)
	}
	for i := range positives.Cases {
		positives.Cases[i].Expect["expect_low_relevance"] = false
	}
	combined := &eval.Dataset{Name: "cove-docs-relevance", Cases: append(negatives.Cases, positives.Cases...)}

	e := &rag.Evaluator{
		Runner:  &rag.RetrievalRunner{Retriever: retriever, TopK: 5},
		Scorers: []rag.Scorer{rag.LowRelevanceIs()},
	}
	rep, err := e.Run(ctx, combined)
	if err != nil {
		t.Fatalf("eval run: %v", err)
	}
	_ = rep.WriteTable(logWriter{t})
	agg := rep.Scorers["low_relevance"]
	t.Logf("low_relevance: pass=%d fail=%d (域外 %d 条 + 域内 %d 条)", agg.Passed, agg.Failed, len(negatives.Cases), len(positives.Cases))
	if rep.PassRate != 1 {
		t.Errorf("低相关判定 pass_rate = %.2f%%, want 100%%", rep.PassRate*100)
	}
}

// TestRealDataNegativeCalibration 打印域内/域外问题的最高向量分,用于校准低相关阈值。
//
// 只有当域外问题的分数明显低于域内问题时,低相关判定(以及基于它的负例门禁)才有意义。
// 词形 HashEmbedder 下两者区间重叠、无可分阈值;真实语义向量下应拉开距离。
//
//	go test ./internal/eval/rag/ -tags ragreal -run TestRealDataNegativeCalibration -v
func TestRealDataNegativeCalibration(t *testing.T) {
	ctx := context.Background()
	dense, keyword, setup := setupRealStores(ctx, t)
	t.Logf("向量模型: %s", setup.name)

	repo := ragchunk.NewRepository(dense, keyword)
	searcher := ragsearch.NewSearcher[models.RAGChunkSource](dense, keyword,
		ragsearch.WithEmbeddingDim(setup.dim),
		ragsearch.WithEmbedder(setup.embedder),
		ragsearch.WithLowRelevanceThreshold(setup.lowThresh),
		ragsearch.WithSourceDecoder[models.RAGChunkSource](repo.DecodeSource))
	retriever := ragadapter.SearcherRetriever{
		Searcher: searcher,
		Embedder: setup.embedder,
		Filter:   vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("user_id", evalUser.String())}},
	}

	collect := func(path, label string) (lo, hi float64) {
		ds, err := eval.LoadDataset(path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		lo, hi = 1e9, -1e9
		for _, c := range ds.Cases {
			_, rel, err := retriever.RetrieveWithRelevance(ctx, c.Query, 5)
			if err != nil {
				t.Fatalf("retrieve %s: %v", c.ID, err)
			}
			score := 0.0
			if rel.MaxScore != nil {
				score = *rel.MaxScore
			}
			lo, hi = min(lo, score), max(hi, score)
			t.Logf("%-8s [%-22s] max_score=%.4f  %q", label, c.ID, score, c.Query)
		}
		return lo, hi
	}

	posLo, posHi := collect("testdata/datasets/cove-docs.json", "域内")
	negLo, negHi := collect("testdata/datasets/cove-docs-negatives.json", "域外")

	t.Logf("域内区间 [%.4f, %.4f] | 域外区间 [%.4f, %.4f]", posLo, posHi, negLo, negHi)
	if negHi < posLo {
		t.Logf("✓ 可分:建议低相关阈值取 (%.4f, %.4f) 之间,例如 %.4f", negHi, posLo, (negHi+posLo)/2)
	} else {
		t.Logf("✗ 不可分:域外最高分 %.4f >= 域内最低分 %.4f,不存在无误判的阈值(负例门禁不应启用)", negHi, posLo)
	}
}

// TestRealDataWeightComparison 用同一真实语料与数据集横向对比不同融合权重,
// 演示 RAG 调参的主用途:量化"向量权重调高/调低"对检索质量的实际影响。
//
//	go test ./internal/eval/rag/ -tags ragreal -run TestRealDataWeightComparison -v
func TestRealDataWeightComparison(t *testing.T) {
	ctx := context.Background()
	dense, keyword, setup := setupRealStores(ctx, t)
	t.Logf("向量模型: %s", setup.name)
	repo := ragchunk.NewRepository(dense, keyword)
	filter := vectorstore.Filter{Must: []vectorstore.Condition{vectorstore.Eq("user_id", evalUser.String())}}

	// 每个变体是一套独立的融合权重(权重属构造级配置)。
	newVariant := func(name string, vectorWeight, bm25Weight float64) rag.Variant {
		searcher := ragsearch.NewSearcher[models.RAGChunkSource](dense, keyword,
			ragsearch.WithEmbeddingDim(setup.dim),
			ragsearch.WithEmbedder(setup.embedder),
			ragsearch.WithVectorWeight(vectorWeight),
			ragsearch.WithBM25Weight(bm25Weight),
			ragsearch.WithSourceDecoder[models.RAGChunkSource](repo.DecodeSource))
		return rag.Variant{
			Name: name,
			Runner: &rag.RetrievalRunner{
				Retriever: ragadapter.SearcherRetriever{Searcher: searcher, Embedder: setup.embedder, Filter: filter},
				TopK:      5,
			},
		}
	}

	ds, err := loadDatasetWithThresholds(t, "testdata/datasets/cove-docs.json", setup)
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

	// 均值差必须配上置信区间才可解读:当前数据集只有十余条用例,一条用例的排名变化
	// 就能让均值波动数个百分点。只有标"显著"(区间不跨 0)的差异才足以支撑调参决策。
	t.Log("bm25-heavy 相对 balanced(配对自助 95% 置信区间):")
	sig := 0
	for _, d := range cmp.DeltaWithCI("balanced-0.6/0.4", "bm25-heavy-0.1/0.9", 0.95, 2000, 1) {
		mark := "不显著(与噪声无法区分)"
		if d.Significant {
			mark = "显著"
			sig++
		}
		t.Logf("  %-16s %.4f → %.4f  delta=%+.4f  95%%CI=[%+.6f, %+.6f]  %s",
			d.Scorer, d.Base, d.Candidate, d.Delta, d.CI.Lo, d.CI.Hi, mark)
	}
	t.Logf("共 %d/%d 项指标差异在 %d 条用例下达到统计显著", sig, len(cmp.Variants[0].Report.Scorers), len(ds.Cases))

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
// openStores 连接真实 Qdrant 与 Elasticsearch(按 setup 隔离的 collection/index)。
func openStores(t *testing.T, setup embedderSetup) (*qdrant.DenseIndex, *es.KeywordIndex) {
	t.Helper()
	dense, err := qdrant.NewDenseIndex(env("QDRANT_ADDR", "localhost:6334"), "", false, setup.collection)
	if err != nil {
		t.Fatalf("qdrant: %v (是否已起 cove-eval-qdrant?)", err)
	}
	esClient, err := es.NewClient(es.Config{URL: env("ES_URL", "http://localhost:9200")})
	if err != nil {
		t.Fatalf("es client: %v (是否已起 cove-eval-es?)", err)
	}
	return dense, es.NewKeywordIndex(esClient, setup.collection)
}

// ingestCorpus 把冻结语料经生产链路灌入检索存储。
func ingestCorpus(ctx context.Context, t *testing.T, setup embedderSetup, dense *qdrant.DenseIndex, keyword *es.KeywordIndex) {
	t.Helper()
	docs, err := corpus.LoadFiles(corpusPaths(), corpus.LoadOptions{UserID: evalUser, Root: corpusDir})
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(docs) != len(corpusFiles) {
		t.Fatalf("loaded %d docs, want %d", len(docs), len(corpusFiles))
	}
	ingester := &corpus.Ingester{Dense: dense, Keyword: keyword, Embedder: setup.embedder, Dim: setup.dim}
	chunks, err := ingester.Ingest(ctx, docs)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	t.Logf("已把 %d 篇真实文档灌入 Qdrant+ES,共 %d 个 chunk", len(docs), chunks)
	waitSearchable(ctx, t, keyword)
}

// setupRealStores 复位并灌入语料,返回可用于检索的存储与嵌入器。
func setupRealStores(ctx context.Context, t *testing.T) (*qdrant.DenseIndex, *es.KeywordIndex, embedderSetup) {
	t.Helper()
	setup := resolveEmbedder(t)
	dense, keyword := openStores(t, setup)
	resetStores(t, setup)
	ingestCorpus(ctx, t, setup, dense, keyword)
	return dense, keyword, setup
}

// resetStores 删除评测专用的 ES 索引与 Qdrant collection,使每次评测从干净状态开始。
//
// 二者随后会由 Ingester 的 EnsureIndex/EnsureCollection 重建。删除不存在的索引返回 404,
// 属正常情形,不视为失败。
func resetStores(t *testing.T, setup embedderSetup) {
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
	del(env("ES_URL", "http://localhost:9200") + "/" + setup.collection)
	del(env("QDRANT_HTTP", "http://localhost:6333") + "/collections/" + setup.collection)
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
