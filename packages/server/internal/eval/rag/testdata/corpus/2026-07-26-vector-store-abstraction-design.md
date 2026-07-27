# 向量存储抽象与 Qdrant 接入设计

日期: 2026-07-26 · 分支: dev · 方案: A（两个窄端口 + core 编排）

## 背景与问题

`internal/core/rag/search.Searcher` 是混合检索引擎，但它直接构造 Elasticsearch
查询 DSL（`knn` / `num_candidates` / `bool.must.match`）并解析 ES 响应
（`_source` / `_score` / `_id`）；`ESClient` 接口是 ES 形状的
（`Search(index, query any) (map[string]any, error)`）。ES 的 `term/terms`
过滤 DSL 甚至泄漏到 logic 层（`documentSearchFilters` / `imageSearchFilters`）。

要求：**core 只保留抽象、不耦合任何具体数据库**，并接入一款专业向量数据库。

## 决策（已确认）

1. 专业向量库选型：**Qdrant**（单容器易运维、payload 过滤强、官方 Go SDK 成熟、
   延迟稳定；对比 Milvus 运维重、pgvector 非专业独立库、Weaviate 一体化混检在本方案用不上）。
2. 混合检索策略：**Qdrant 负责 dense 向量，ES 保留 BM25 关键词**，core 做两路融合（双存储）。
3. 覆盖范围：**RAG 读 + 写路径统一**（检索 + 索引/写入/删除/EnsureIndex 全部走中立抽象）。

## 目标架构

```
写: chunk(text+vector+meta) ─▶ DenseIndex(Qdrant: vector+payload)
                           └─▶ KeywordIndex(ES: text+fields)
读: query ─embed─▶ DenseIndex.Search(vector,k,filter) ┐
    query ────────▶ KeywordIndex.Search(text,k,filter) ┼─▶ core 融合/rerank/相关度 ─▶ topK
    parent 内容 ──▶ KeywordIndex.Fetch(filter=In(chunk_id,...))
```

core 只依赖中立端口 + 中立 DTO，不出现任何 ES / Qdrant 类型或查询 DSL。

## 中立抽象（新包 `internal/core/rag/vectorstore`）

```go
type Op string
const ( OpEq Op = "eq"; OpIn Op = "in" )
type Condition struct { Field string; Op Op; Value any }
type Filter    struct { Must, MustNot []Condition }
func Eq(field string, v any) Condition
func In(field string, values any) Condition

type Point struct { ID string; Vector []float64; Fields map[string]any } // 写
type Hit   struct { ID string; Score float64;   Fields map[string]any }  // 读

type DenseIndex interface {   // Qdrant 适配器
    EnsureCollection(ctx, dim int) error
    Upsert(ctx, points []Point) error
    DeleteByFilter(ctx, f Filter) error
    SetFields(ctx, f Filter, fields map[string]any) error
    Search(ctx, vector []float64, k int, f Filter) ([]Hit, error)
}
type KeywordIndex interface { // ES 适配器
    EnsureIndex(ctx) error
    Index(ctx, docs []Point) error
    DeleteByFilter(ctx, f Filter) error
    SetFields(ctx, f Filter, fields map[string]any) error
    Search(ctx, query string, k int, f Filter) ([]Hit, error)
    Fetch(ctx, f Filter, size int) ([]Hit, error)
}
```

## 改动清单

- **core/rag/vectorstore**（新）：上述 DTO + 端口。
- **core/rag/search**：`Searcher` 改依赖 `vectorstore.DenseIndex` + `KeywordIndex` 的读方法；
  删除 `vectorQuery/bm25Query/responseHits/collectHits` 等 ES 专用逻辑；`Input.Filters []any`
  改为 `vectorstore.Filter`；`SourceDecoder`/`RerankDocumentBuilder` 继续吃中立 `Fields map[string]any`。
  融合/rerank/相关度逻辑不变（本就基于 id→fields + score map）。
- **infrastructure/db/es 适配器**：实现 `KeywordIndex`，中立 `Filter`→ES DSL，BM25 query，
  响应→`Hit`；承接 EnsureIndex/Index/Delete/SetFields/Fetch。
- **infrastructure/db/qdrant 适配器**（新，`github.com/qdrant/go-client`）：实现 `DenseIndex`。
- **repository/es/rag_chunk**：写路径向 DenseIndex + KeywordIndex 扇出。
- **logic/document、logic/image**：`documentSearchFilters/imageSearchFilters` 返回中立 `Filter`。
- **svc/context、config**：装配两适配器 + Qdrant 连接配置。

## 一致性与边界

- 双写：doc 索引先写 Qdrant（vector+payload）再写 ES（text+fields），任一失败返回错误，
  上层重试（现有语义即整体失败）。删除/改字段同样双向执行。
- ID 一致：两存储用同一 `chunk_id`（确定性 UUID），保证融合按 ID 对齐。
- parent 内容：内容存于 ES；`KeywordIndex.Fetch(In(chunk_id, parentIDs))` 批量取回。
- 过滤：当前仅 `user_id/source_type/tags` 的 Eq/In，`Filter` 谓词已覆盖。

## 测试

- vectorstore：Filter 构造/谓词单测。
- search：用实现中立端口的 fake 替换原 `fakeESClient`，覆盖融合/门槛/rerank/parent。
- es 适配器：Filter→DSL 翻译单测。
- Qdrant 适配器：Filter→qdrant 条件翻译单测（不依赖真实实例）；E2E 需运行 Qdrant，另行验证。
