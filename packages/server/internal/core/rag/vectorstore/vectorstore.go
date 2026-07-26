// Package vectorstore 定义 RAG 检索与索引所需的数据库中立抽象。
//
// core 只依赖本包的接口与 DTO，不出现任何具体存储（Elasticsearch、Qdrant 等）的
// 类型或查询 DSL。具体存储由 infrastructure 层的适配器实现这些端口。
package vectorstore

import "context"

// Op 是中立过滤谓词的操作符。
type Op string

const (
	// OpEq 表示字段等于给定标量值。
	OpEq Op = "eq"
	// OpIn 表示字段命中给定集合中的任意值。
	OpIn Op = "in"
)

// Condition 是单个中立过滤谓词。
//
// Value 语义由 Op 决定：OpEq 为标量，OpIn 为切片（[]string 等）。
type Condition struct {
	Field string
	Op    Op
	Value any
}

// Filter 是一次检索/删除的中立过滤条件。
//
// Must 内的条件全部满足；MustNot 内的条件全部不满足。适配器负责翻译到各自的查询语言。
type Filter struct {
	Must    []Condition
	MustNot []Condition
}

// Eq 构造一个字段等值谓词。
func Eq(field string, value any) Condition {
	return Condition{Field: field, Op: OpEq, Value: value}
}

// In 构造一个字段集合命中谓词。
func In(field string, values any) Condition {
	return Condition{Field: field, Op: OpIn, Value: values}
}

// Point 是一次写入的中立记录。
//
// Vector 为稠密向量（关键词存储可忽略）；Fields 为业务元数据与内容字段。
type Point struct {
	ID     string
	Vector []float64
	Fields map[string]any
}

// Hit 是一次召回命中的中立表示。
//
// Score 尺度由具体存储决定（向量为相似度、关键词为 BM25 分）；上层只做相对融合。
// Fields 是命中记录的业务字段，由 SourceDecoder 等按需解码。
type Hit struct {
	ID     string
	Score  float64
	Fields map[string]any
}

// DenseIndex 是稠密向量存储端口（读 + 写），由 Qdrant 等适配器实现。
type DenseIndex interface {
	// EnsureCollection 确保维度为 dim 的向量集合存在。
	EnsureCollection(ctx context.Context, dim int) error
	// Upsert 按 ID 幂等写入若干向量点。
	Upsert(ctx context.Context, points []Point) error
	// DeleteByFilter 删除命中过滤条件的全部点。
	DeleteByFilter(ctx context.Context, filter Filter) error
	// SetFields 为命中过滤条件的点批量更新给定字段。
	SetFields(ctx context.Context, filter Filter, fields map[string]any) error
	// Search 返回按向量相似度排序的前 k 个命中，filter 参与召回过滤。
	//
	// 约定 Hit.Score 为 cosine 相似度（范围 [-1,1]），上层可直接据此做相关度门控，
	// 无需了解具体存储的打分方式。
	Search(ctx context.Context, vector []float64, k int, filter Filter) ([]Hit, error)
}

// KeywordIndex 是关键词/BM25 存储端口（读 + 写），由 Elasticsearch 等适配器实现。
type KeywordIndex interface {
	// EnsureIndex 确保关键词索引存在。
	EnsureIndex(ctx context.Context) error
	// Index 写入若干文档（Vector 可为空）。
	Index(ctx context.Context, docs []Point) error
	// DeleteByFilter 删除命中过滤条件的全部文档。
	DeleteByFilter(ctx context.Context, filter Filter) error
	// SetFields 为命中过滤条件的文档批量更新给定字段。
	SetFields(ctx context.Context, filter Filter, fields map[string]any) error
	// Search 返回按 BM25 相关度排序的前 k 个命中，filter 参与召回过滤。
	Search(ctx context.Context, query string, k int, filter Filter) ([]Hit, error)
	// Fetch 按过滤条件直接取回文档（用于 parent 内容批量回填等），不做相关度排序。
	Fetch(ctx context.Context, filter Filter, size int) ([]Hit, error)
}
