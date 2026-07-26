// Package prompt 内置记忆流程的默认提示词模板资源。
//
// 与 rag/prompt 一致：本包只声明模板文件与模板名称，读取/渲染交给 core/prompt。
// 模板变量直接由 core/memory 的领域输入类型（StatementPromptInput 等）提供，
// 因此本包不再重复声明参数结构，也不依赖 core/memory，避免 import 环。
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
