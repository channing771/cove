package memory

import (
	"log/slog"

	"github.com/boxify/api-go/internal/core/id"
	"github.com/boxify/api-go/internal/core/jsonx"
	"github.com/boxify/api-go/internal/core/llm"
)

// Deps 汇聚记忆各引擎（萃取编排、社区聚类）的公共依赖。
//
// 通过统一的 Option 配置，让不同引擎复用同一套 With 选项，避免逐个引擎重复定义。
// 具体引擎按需取用其中字段（如萃取编排不使用 Community）。
type Deps struct {
	Config     Config
	LLM        llm.Client
	IDGen      id.Generator
	JSONParser jsonx.Parser
	Prompter   Prompter
	Graph      GraphStore
	Community  CommunityStore
	Logger     *slog.Logger
}

// Option 配置 Deps。
type Option func(*Deps)

// WithConfig 设置 memory 调参项。
func WithConfig(cfg Config) Option {
	return func(d *Deps) { d.Config = cfg }
}

// WithLLM 设置向量化、判定与生成使用的模型客户端。
func WithLLM(client llm.Client) Option {
	return func(d *Deps) { d.LLM = client }
}

// WithIDGenerator 设置 ID 生成器。
func WithIDGenerator(generator id.Generator) Option {
	return func(d *Deps) { d.IDGen = generator }
}

// WithJSONParser 设置 LLM 输出的 JSON 解析器。
func WithJSONParser(parser jsonx.Parser) Option {
	return func(d *Deps) { d.JSONParser = parser }
}

// WithGraphStore 设置图存储端口。
func WithGraphStore(graph GraphStore) Option {
	return func(d *Deps) { d.Graph = graph }
}

// WithCommunityStore 设置社区存储端口。
func WithCommunityStore(community CommunityStore) Option {
	return func(d *Deps) { d.Community = community }
}

// WithPrompter 覆盖默认内置提示词实现；传 nil 时忽略。
func WithPrompter(prompter Prompter) Option {
	return func(d *Deps) {
		if prompter != nil {
			d.Prompter = prompter
		}
	}
}

// WithLogger 注入日志器；传 nil 时忽略。
func WithLogger(logger *slog.Logger) Option {
	return func(d *Deps) {
		if logger != nil {
			d.Logger = logger
		}
	}
}

// ResolveDeps 应用选项并补齐默认值：提示词默认使用内置实现，日志器默认 slog.Default()。
func ResolveDeps(opts ...Option) Deps {
	deps := Deps{
		Prompter: NewBuiltinPrompter(),
		Logger:   slog.Default(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&deps)
		}
	}
	return deps
}
