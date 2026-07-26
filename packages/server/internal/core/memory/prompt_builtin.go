package memory

import (
	mprompt "github.com/boxify/api-go/internal/core/memory/prompt"
	coreprompt "github.com/boxify/api-go/internal/core/prompt"
)

// builtinPrompter 使用 core/memory/prompt 内置模板实现 Prompter。
//
// 模板变量直接取自领域输入类型（StatementPromptInput 等），无需另立参数结构。
// 记忆模块因此自带提示词，无需外部注入（与 rag/classifier 等模块一致）。
type builtinPrompter struct{}

// NewBuiltinPrompter 返回基于内置模板的默认 Prompter。
func NewBuiltinPrompter() Prompter { return builtinPrompter{} }

var _ Prompter = builtinPrompter{}

// StatementExtract 渲染原子陈述抽取提示词。
func (builtinPrompter) StatementExtract(input *StatementPromptInput) (string, error) {
	if input == nil {
		input = &StatementPromptInput{}
	}
	return coreprompt.Render(mprompt.Templates, mprompt.StatementExtractTemplate, input)
}

// TripletExtract 渲染实体和三元组抽取提示词。
func (builtinPrompter) TripletExtract(input *TripletPromptInput) (string, error) {
	if input == nil {
		input = &TripletPromptInput{}
	}
	return coreprompt.Render(mprompt.Templates, mprompt.TripletExtractTemplate, input)
}

// DedupEntity 渲染实体去重判断提示词。
func (builtinPrompter) DedupEntity(input *DedupPromptInput) (string, error) {
	if input == nil {
		input = &DedupPromptInput{}
	}
	return coreprompt.Render(mprompt.Templates, mprompt.DedupEntityTemplate, input)
}

// GenerateCommunityMetadata 渲染社区名称和摘要生成提示词。
func (builtinPrompter) GenerateCommunityMetadata(input *CommunityMetadataPromptInput) (string, error) {
	if input == nil {
		input = &CommunityMetadataPromptInput{}
	}
	return coreprompt.Render(mprompt.Templates, mprompt.CommunityMetadataTemplate, input)
}
