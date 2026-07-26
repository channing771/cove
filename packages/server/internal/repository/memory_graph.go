/**
 * @Time   : 2026/6/23 22:58
 * @Author : chenyangzhao542@gmail.com
 * @File   : memory_graph.go
 **/

package repository

import (
	"github.com/boxify/api-go/internal/core/memory"
)

// MemoryGraphRepository 是记忆图谱仓库契约。
//
// 单一来源定义在 core/memory.GraphStore；此处以类型别名对齐应用层的仓库命名，
// 保证 core/memory 只依赖自身抽象、依赖方向由 repository 指向 core。
type MemoryGraphRepository = memory.GraphStore
