// Package prompt 内置记忆流程的默认提示词模板与模板参数结构。
//
// 与 rag/prompt 一致：本包只声明模板文件与模板变量，读取/渲染交给 core/prompt。
// 模块自带模板意味着 core/memory 无需外部注入 Prompter。本包不依赖 core/memory，
// 避免与其形成 import 环。
package prompt

import "embed"

// Templates 暴露记忆流程默认提示词模板文件。
//
//go:embed *.tmpl
var Templates embed.FS

const (
	// StatementExtractTemplate 原子陈述抽取模板文件名。
	StatementExtractTemplate = "extract_statement.tmpl"
	// TripletExtractTemplate 实体与三元组抽取模板文件名。
	TripletExtractTemplate = "extract_triplet.tmpl"
	// DedupEntityTemplate 实体去重判断模板文件名。
	DedupEntityTemplate = "dedup_entity.tmpl"
	// CommunityMetadataTemplate 社区名称与摘要生成模板文件名。
	CommunityMetadataTemplate = "generate_community_metadata.tmpl"
)

// StatementExtractData 约束原子陈述抽取模板可用变量。
type StatementExtractData struct {
	Content string
	Context string
}

// TripletExtractData 约束实体与三元组抽取模板可用变量。
type TripletExtractData struct {
	Statement   string
	Context     string
	EntityTypes []string
	Predicates  []string
	ValidAt     string
	InvalidAt   string
	DialogAt    string
}

// DedupEntityData 约束实体去重模板可用变量。
type DedupEntityData struct {
	EntityA DedupEntitySide
	EntityB DedupEntitySide
	Context DedupSimilarity
}

// DedupEntitySide 表示去重判断中的一个候选实体。
type DedupEntitySide struct {
	Name        string
	Type        string
	Description string
	Aliases     []string
}

// DedupSimilarity 表示去重判断的相似度特征（模板按字符串渲染）。
type DedupSimilarity struct {
	NameTextSim  string
	NameEmbedSim string
	NameContains string
}

// CommunityMetadataData 约束社区元数据生成模板可用变量。
type CommunityMetadataData struct {
	Members []CommunityMetadataMember
}

// CommunityMetadataMember 表示社区摘要中的一个成员。
type CommunityMetadataMember struct {
	Name        string
	Description string
}
