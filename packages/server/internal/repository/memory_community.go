/**
 * @Time   : 2026/6/24 23:47
 * @Author : chenyangzhao542@gmail.com
 * @File   : memory_community.go
 **/

package repository

import (
	"github.com/boxify/api-go/internal/core/memory"
)

// MemoryCommunityRepository 是社区聚类仓库契约。
//
// 单一来源定义在 core/memory.CommunityStore；此处以类型别名对齐应用层的仓库命名。
type MemoryCommunityRepository = memory.CommunityStore
