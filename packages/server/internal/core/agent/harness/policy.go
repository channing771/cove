package harness

import (
	"context"
	"fmt"

	coretool "github.com/boxify/api-go/internal/core/tool"
)

// PolicyMode 决定未授权工具的处理方式。
type PolicyMode int

const (
	// PolicyRefuse 返回一条拒绝观察结果，让 agent 自适应换工具（默认）。
	PolicyRefuse PolicyMode = iota
	// PolicyHardStop 直接返回 ErrToolDenied 终止运行。
	PolicyHardStop
)

// Authorizer 判断某次工具调用是否被允许。
type Authorizer func(ctx context.Context, tool string, input coretool.Input) bool

// Policy 描述工具治理策略。
//
// Allowlist 用于建 registry 时静态过滤（空表示不限制）。Authorize 为运行期动态门禁。
type Policy struct {
	Allowlist []string
	Mode      PolicyMode
	Authorize Authorizer
}

// allowed 判断工具名是否在白名单内。
func (p Policy) allowed(name string) bool {
	if len(p.Allowlist) == 0 {
		return true
	}
	for _, a := range p.Allowlist {
		if a == name {
			return true
		}
	}
	return false
}

// guardTool 用动态授权器包裹工具。Authorize 为 nil 时原样返回。
func guardTool(t coretool.Tool, p Policy) coretool.Tool {
	if p.Authorize == nil {
		return t
	}
	return &guardedTool{inner: t, policy: p}
}

type guardedTool struct {
	inner  coretool.Tool
	policy Policy
}

func (g *guardedTool) Describe(ctx context.Context) (coretool.Descriptor, error) {
	return g.inner.Describe(ctx)
}

func (g *guardedTool) Invoke(ctx context.Context, input coretool.Input) (coretool.Output, error) {
	d, _ := g.inner.Describe(ctx)
	if g.policy.Authorize(ctx, d.Name, input) {
		return g.inner.Invoke(ctx, input)
	}
	if g.policy.Mode == PolicyHardStop {
		return coretool.Output{}, ErrToolDenied
	}
	return coretool.Output{Text: fmt.Sprintf("tool %q is not permitted by policy", d.Name)}, nil
}
