// Package prompts 集中提供业务层提示词模板及其注册入口。
//
// 模板文件在本包目录内平铺存放，调用方通过 Register 将模板注册到 core/prompt.Manager。
// 物理文件名与逻辑名称由本包显式映射，业务调用方无需了解模板文件路径。
package prompts

import (
	"embed"
	"fmt"
	"io/fs"

	coreprompt "github.com/boxify/api-go/internal/core/prompt"
)

//go:embed *.tmpl
var templates embed.FS

type definition struct {
	name string
	file string
}

func registeredPrompts() []definition {
	return []definition{
		{name: "agent/optimize_prompt", file: "optimize_prompt.tmpl"},
		{name: "skill/optimize_prompt", file: "optimize_skill_prompt.tmpl"},
	}
}

// Register 将内置业务提示词注册到 manager。
//
// manager 不能为 nil。注册完成后，调用方可继续使用 agent/... 和 skill/... 逻辑名称
// 读取或渲染模板。记忆流程的提示词已内置在 core/memory/prompt，不再经此注册。
// 读取嵌入模板或注册模板失败时，Register 返回包含逻辑名称的错误。
func Register(manager *coreprompt.Manager) error {
	if manager == nil {
		return fmt.Errorf("prompt manager is nil")
	}

	for _, item := range registeredPrompts() {
		text, err := fs.ReadFile(templates, item.file)
		if err != nil {
			return fmt.Errorf("read prompt %s failed: %w", item.name, err)
		}
		if err := manager.RegisterText(item.name, string(text)); err != nil {
			return fmt.Errorf("register prompt %s failed: %w", item.name, err)
		}
	}
	return nil
}
