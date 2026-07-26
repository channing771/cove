// Package qdrant 用 Qdrant 实现 vectorstore.DenseIndex（稠密向量存储：建库/写入/删除/检索）。
//
// 它把中立的 vectorstore.Filter/Point/Hit 翻译到 Qdrant gRPC 类型，
// 使 core 检索层无需了解任何 Qdrant 细节。
package qdrant

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	qc "github.com/qdrant/go-client/qdrant"

	"github.com/boxify/api-go/internal/core/rag/vectorstore"
	"github.com/boxify/api-go/internal/xerr"
)

// 需要建立 keyword payload 索引的过滤字段（保证 filter 检索高效）。
var payloadIndexFields = []string{"user_id", "source_id", "kb_id", "source_type", "tags", "chunk_id", "parent_id"}

// DenseIndex 是 Qdrant 稠密向量存储适配器。
type DenseIndex struct {
	client     *qc.Client
	collection string
}

// NewDenseIndex 连接 Qdrant 并返回稠密存储适配器。
//
// addr 为 gRPC 地址 host:port（缺省端口 6334）。
func NewDenseIndex(addr, apiKey string, useTLS bool, collection string) (*DenseIndex, error) {
	host, port, err := splitAddr(addr)
	if err != nil {
		return nil, err
	}
	collection = strings.TrimSpace(collection)
	if collection == "" {
		collection = "cove_chunks"
	}
	client, err := qc.NewClient(&qc.Config{
		Host:   host,
		Port:   port,
		APIKey: apiKey,
		UseTLS: useTLS,
	})
	if err != nil {
		return nil, xerr.Wrapf(err, "创建 Qdrant 客户端失败")
	}
	return &DenseIndex{client: client, collection: collection}, nil
}

// EnsureCollection 确保维度为 dim 的 cosine 向量集合存在，并为过滤字段建立 keyword payload 索引。
func (d *DenseIndex) EnsureCollection(ctx context.Context, dim int) error {
	if dim <= 0 {
		dim = 1024
	}
	exists, err := d.client.CollectionExists(ctx, d.collection)
	if err != nil {
		return xerr.Wrapf(err, "检查 Qdrant 集合失败: %s", d.collection)
	}
	if !exists {
		err = d.client.CreateCollection(ctx, &qc.CreateCollection{
			CollectionName: d.collection,
			VectorsConfig: qc.NewVectorsConfig(&qc.VectorParams{
				Size:     uint64(dim),
				Distance: qc.Distance_Cosine,
			}),
		})
		if err != nil {
			return xerr.Wrapf(err, "创建 Qdrant 集合失败: %s", d.collection)
		}
	}
	// payload 索引幂等创建；已存在时 Qdrant 返回成功。
	for _, field := range payloadIndexFields {
		if _, err := d.client.CreateFieldIndex(ctx, &qc.CreateFieldIndexCollection{
			CollectionName: d.collection,
			FieldName:      field,
			FieldType:      qc.FieldType_FieldTypeKeyword.Enum(),
		}); err != nil {
			return xerr.Wrapf(err, "创建 Qdrant payload 索引失败: %s", field)
		}
	}
	return nil
}

// Upsert 按 ID 幂等写入若干向量点（向量 + payload）。
func (d *DenseIndex) Upsert(ctx context.Context, points []vectorstore.Point) error {
	if len(points) == 0 {
		return nil
	}
	qpoints := make([]*qc.PointStruct, 0, len(points))
	for _, point := range points {
		if point.ID == "" {
			continue
		}
		payload, err := payloadFromFields(point.Fields)
		if err != nil {
			return err
		}
		qpoints = append(qpoints, &qc.PointStruct{
			Id:      qc.NewID(point.ID),
			Vectors: qc.NewVectorsDense(toFloat32(point.Vector)),
			Payload: payload,
		})
	}
	_, err := d.client.Upsert(ctx, &qc.UpsertPoints{
		CollectionName: d.collection,
		Points:         qpoints,
	})
	if err != nil {
		return xerr.Wrapf(err, "写入 Qdrant 向量失败")
	}
	return nil
}

// DeleteByFilter 删除命中过滤条件的全部点。
func (d *DenseIndex) DeleteByFilter(ctx context.Context, filter vectorstore.Filter) error {
	_, err := d.client.Delete(ctx, &qc.DeletePoints{
		CollectionName: d.collection,
		Points:         qc.NewPointsSelectorFilter(qdrantFilter(filter)),
	})
	if err != nil {
		return xerr.Wrapf(err, "删除 Qdrant 向量失败")
	}
	return nil
}

// SetFields 为命中过滤条件的点批量更新给定 payload 字段。
func (d *DenseIndex) SetFields(ctx context.Context, filter vectorstore.Filter, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	payload, err := payloadFromFields(fields)
	if err != nil {
		return err
	}
	_, err = d.client.SetPayload(ctx, &qc.SetPayloadPoints{
		CollectionName: d.collection,
		Payload:        payload,
		PointsSelector: qc.NewPointsSelectorFilter(qdrantFilter(filter)),
	})
	if err != nil {
		return xerr.Wrapf(err, "更新 Qdrant payload 失败")
	}
	return nil
}

// Search 返回按 cosine 相似度排序的前 k 个命中，Hit.Score 即 cosine 相似度。
func (d *DenseIndex) Search(ctx context.Context, vector []float64, k int, filter vectorstore.Filter) ([]vectorstore.Hit, error) {
	limit := uint64(k)
	scored, err := d.client.Query(ctx, &qc.QueryPoints{
		CollectionName: d.collection,
		Query:          qc.NewQueryDense(toFloat32(vector)),
		Filter:         qdrantFilter(filter),
		Limit:          &limit,
		WithPayload:    qc.NewWithPayloadEnable(true),
	})
	if err != nil {
		return nil, xerr.Wrapf(err, "查询 Qdrant 失败")
	}
	hits := make([]vectorstore.Hit, 0, len(scored))
	for _, point := range scored {
		id := pointID(point.GetId())
		if id == "" {
			continue
		}
		hits = append(hits, vectorstore.Hit{
			ID:     id,
			Score:  float64(point.GetScore()),
			Fields: payloadToFields(point.GetPayload()),
		})
	}
	return hits, nil
}

// qdrantFilter 把中立 Filter 翻译成 Qdrant Filter；空条件返回 nil（全量）。
func qdrantFilter(filter vectorstore.Filter) *qc.Filter {
	must := qdrantConditions(filter.Must)
	mustNot := qdrantConditions(filter.MustNot)
	if len(must) == 0 && len(mustNot) == 0 {
		return nil
	}
	return &qc.Filter{Must: must, MustNot: mustNot}
}

// qdrantConditions 把中立谓词翻译成 Qdrant keyword match 条件。
func qdrantConditions(conditions []vectorstore.Condition) []*qc.Condition {
	if len(conditions) == 0 {
		return nil
	}
	out := make([]*qc.Condition, 0, len(conditions))
	for _, condition := range conditions {
		switch condition.Op {
		case vectorstore.OpIn:
			out = append(out, qc.NewMatchKeywords(condition.Field, toStrings(condition.Value)...))
		default:
			out = append(out, qc.NewMatchKeyword(condition.Field, fmt.Sprint(condition.Value)))
		}
	}
	return out
}

// payloadFromFields 把中立字段转换成 Qdrant payload，兼容 []string 等切片类型。
func payloadFromFields(fields map[string]any) (map[string]*qc.Value, error) {
	if len(fields) == 0 {
		return map[string]*qc.Value{}, nil
	}
	normalized := make(map[string]any, len(fields))
	for key, value := range fields {
		normalized[key] = normalizeValue(value)
	}
	payload, err := qc.TryValueMap(normalized)
	if err != nil {
		return nil, xerr.Wrapf(err, "构造 Qdrant payload 失败")
	}
	return payload, nil
}

// normalizeValue 把 NewValue 不直接支持的类型（如 []string）转换成兼容形式。
func normalizeValue(value any) any {
	switch v := value.(type) {
	case []string:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out
	case []float64:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out
	default:
		return value
	}
}

// payloadToFields 把 Qdrant payload 翻译回中立字段。
func payloadToFields(payload map[string]*qc.Value) map[string]any {
	fields := make(map[string]any, len(payload))
	for key, value := range payload {
		fields[key] = valueToAny(value)
	}
	return fields
}

// valueToAny 把 Qdrant Value 转换为 Go 原生值。
func valueToAny(value *qc.Value) any {
	switch value.GetKind().(type) {
	case *qc.Value_StringValue:
		return value.GetStringValue()
	case *qc.Value_IntegerValue:
		return value.GetIntegerValue()
	case *qc.Value_DoubleValue:
		return value.GetDoubleValue()
	case *qc.Value_BoolValue:
		return value.GetBoolValue()
	case *qc.Value_ListValue:
		items := value.GetListValue().GetValues()
		out := make([]any, 0, len(items))
		for _, item := range items {
			out = append(out, valueToAny(item))
		}
		return out
	default:
		return nil
	}
}

// pointID 从 Qdrant PointId 取回字符串 id（写入时用 uuid）。
func pointID(id *qc.PointId) string {
	if id == nil {
		return ""
	}
	if uuid := id.GetUuid(); uuid != "" {
		return uuid
	}
	if num := id.GetNum(); num != 0 {
		return strconv.FormatUint(num, 10)
	}
	return ""
}

// toFloat32 把 float64 向量转换为 Qdrant 需要的 float32。
func toFloat32(vector []float64) []float32 {
	out := make([]float32, len(vector))
	for i, value := range vector {
		out[i] = float32(value)
	}
	return out
}

// toStrings 把 OpIn 的集合值转换成 []string。
func toStrings(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return []string{fmt.Sprint(v)}
	}
}

// splitAddr 解析 host:port，缺省端口 6334。
func splitAddr(addr string) (string, int, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "localhost", 6334, nil
	}
	host, portStr, found := strings.Cut(addr, ":")
	if !found {
		return addr, 6334, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, xerr.Wrapf(err, "无效的 Qdrant 地址端口: %s", addr)
	}
	return host, port, nil
}

var _ vectorstore.DenseIndex = (*DenseIndex)(nil)
