package memory

import (
	"strconv"

	mprompt "github.com/boxify/api-go/internal/core/memory/prompt"
	coreprompt "github.com/boxify/api-go/internal/core/prompt"
)

// builtinPrompter 使用 core/memory/prompt 内置模板实现 Prompter。
//
// 记忆模块因此自带提示词，无需外部注入（与 rag/classifier 等模块一致）。
type builtinPrompter struct{}

// NewBuiltinPrompter 返回基于内置模板的默认 Prompter。
func NewBuiltinPrompter() Prompter { return builtinPrompter{} }

var _ Prompter = builtinPrompter{}

// StatementExtract 渲染原子陈述抽取提示词。
func (builtinPrompter) StatementExtract(input *StatementPromptInput) (string, error) {
	data := mprompt.StatementExtractData{}
	if input != nil {
		data.Content = input.Content
		data.Context = input.Context
	}
	return coreprompt.Render(mprompt.Templates, mprompt.StatementExtractTemplate, data)
}

// TripletExtract 渲染实体和三元组抽取提示词。
func (builtinPrompter) TripletExtract(input *TripletPromptInput) (string, error) {
	data := mprompt.TripletExtractData{}
	if input != nil {
		data.Statement = input.Statement
		data.Context = input.Context
		data.EntityTypes = append([]string(nil), input.EntityTypes...)
		data.Predicates = append([]string(nil), input.Predicates...)
		data.ValidAt = input.ValidAt
		data.InvalidAt = input.InvalidAt
		data.DialogAt = input.DialogAt
	}
	return coreprompt.Render(mprompt.Templates, mprompt.TripletExtractTemplate, data)
}

// DedupEntity 渲染实体去重判断提示词。
func (builtinPrompter) DedupEntity(input *DedupPromptInput) (string, error) {
	data := mprompt.DedupEntityData{}
	if input != nil {
		data.EntityA = mprompt.DedupEntitySide{
			Name:        input.EntityA.Name,
			Type:        input.EntityA.Type,
			Description: input.EntityA.Description,
			Aliases:     append([]string(nil), input.EntityA.Aliases...),
		}
		data.EntityB = mprompt.DedupEntitySide{
			Name:        input.EntityB.Name,
			Type:        input.EntityB.Type,
			Description: input.EntityB.Description,
			Aliases:     append([]string(nil), input.EntityB.Aliases...),
		}
		data.Context = mprompt.DedupSimilarity{
			NameTextSim:  strconv.FormatFloat(input.Context.NameTextSim, 'f', -1, 64),
			NameEmbedSim: strconv.FormatFloat(input.Context.NameEmbedSim, 'f', -1, 64),
			NameContains: strconv.FormatBool(input.Context.NameContains),
		}
	}
	return coreprompt.Render(mprompt.Templates, mprompt.DedupEntityTemplate, data)
}

// GenerateCommunityMetadata 渲染社区名称和摘要生成提示词。
func (builtinPrompter) GenerateCommunityMetadata(input *CommunityMetadataPromptInput) (string, error) {
	data := mprompt.CommunityMetadataData{}
	if input != nil {
		data.Members = make([]mprompt.CommunityMetadataMember, 0, len(input.Members))
		for _, member := range input.Members {
			if member == nil {
				continue
			}
			data.Members = append(data.Members, mprompt.CommunityMetadataMember{
				Name:        member.Name,
				Description: member.Description,
			})
		}
	}
	return coreprompt.Render(mprompt.Templates, mprompt.CommunityMetadataTemplate, data)
}
