package agentpersona

import (
	"github.com/boxify/api-go/internal/svc"
	"github.com/boxify/api-go/internal/transport/http/request"
	"github.com/boxify/api-go/internal/xerr"
	"github.com/google/uuid"
)

// avatarURL 生成头像访问 URL。
//
// URLSigner 在本地存储后端(默认)下为 nil，直接解引用会 panic；
// 这里做 nil 与空 key 保护，返回空字符串。
func avatarURL(svcCtx *svc.ServiceContext, avatarKey string) string {
	if svcCtx == nil || svcCtx.URLSigner == nil || avatarKey == "" {
		return ""
	}
	return svcCtx.URLSigner.URL(avatarKey)
}

func personIDFromInput(input *request.UriAgentPersonaIDRequest) (uuid.UUID, error) {
	if input == nil {
		return uuid.Nil, xerr.BadRequest("智能体角色 ID 无效")
	}
	id, err := uuid.Parse(input.PersonaID)
	if err != nil {
		return uuid.Nil, xerr.BadRequest("智能体角色 ID 无效")
	}
	return id, nil
}
