# core 包模块编写约束

`internal/core/**` 是与具体框架、数据库、业务无关的**纯抽象库**。所有 core 模块遵循同一套写法，本文件是这套写法的约束与自检清单。新增或修改 core 模块前请对照执行。

参考范式模块：`rag/search`、`rag/classifier`、`context`、`memory`。

---

## 1. 定位：core 是纯抽象库

- **依赖方向永远是 app → core，绝不反向。** core 不感知它被谁使用。
- core 只允许依赖：
  - Go 标准库；
  - 其他 `internal/core/**` 包；
  - 共享内核 `internal/util`、`internal/xerr`（二者本身不依赖任何应用基础设施，是安全叶子）；
  - 第三方库。
- **禁止**依赖任何应用基础设施：
  `internal/config`、`internal/repository`、`internal/svc`、`internal/logic`、
  `internal/prompts`、`internal/observability`、`internal/models`、
  `internal/infrastructure`、`internal/transport`、`internal/domain`。
- 应用配置、具体仓库、具体数据库 DSL、业务模型都不得进入 core；它们通过下面的“端口 + 选项”从外部注入。

> 反例（已修复）：`memory` 曾直接 import `internal/config` / `internal/repository` /
> `internal/observability/xlog`，是唯一反向依赖应用层的 core 包。现已改为自定义端口 + 注入。

---

## 2. 端口（in-package interface）

- 外部协作者（模型客户端、向量/关键词存储、图存储、reranker 等）一律以**本包内声明的接口**表达，
  由 app 层的具体实现**结构化满足**（无需显式 implements）。
- 端口只声明该模块真正用到的最小方法集。
- 示例：`search.Embedder` / `search.Reranker`、`vectorstore.DenseIndex` / `KeywordIndex`、
  `classifier.TextClient`、`memory.GraphStore` / `CommunityStore`、`tool.Tool` / `ToolSet`。
- 端口若对返回值有语义约定（分数尺度、排序、单位），写进接口方法的文档。
  示例：`vectorstore.DenseIndex.Search` 约定 `Hit.Score` 为 cosine 相似度。

---

## 3. 依赖注入与构造函数

- 构造函数命名 `NewXxx`，签名形如 `NewXxx(必需身份参数, opts ...Option)`。
  - 少量“核心/必需”依赖可作为定位参数（如 `search.NewSearcher(dense, keyword, opts...)`）。
  - 其余协作者、调参、可选依赖一律经 `With` 选项注入。
- **默认值在构造函数里补齐**（默认解析器、默认权重、默认提示词、默认 logger 等）。
- 构造函数不做 I/O、不 panic（除非是不可恢复的编程错误，如必填 map 为 nil）。

---

## 4. 选项（Option）约定

- **构造级选项**：`type Option func(*Options)` + `type Options struct{...}`；
  构造函数内 `for _, opt := range opts { if opt != nil { opt(&x.Options) } }`。
  参考 `rag/search`、`rag/classifier`、`mcp`。
- 允许的**受控变体**（按场景选择，不要滥用）：
  - **多引擎共享依赖**：用 `Deps` 汇聚公共依赖 + `ResolveDeps(opts ...Option) Deps`，
    各构造函数从 `Deps` 取字段。参考 `memory`（萃取与聚类共享一套选项）。
  - **选项直接作用于组件**：`type Option func(*Manager)`，当无需独立 `Options` 结构时。参考 `context`。
  - **一个包内多种选项族**：用领域名区分，如 `channel.RegistryOption`、`tool.RunnerOption`。
- **请求级选项**与构造级分离，命名 `InputOption` / `ModelCallOption`。参考 `search.InputOption`、`llm.ModelCallOption`。
- `With` 函数对 `nil` 或非法值应**忽略并保留默认**（如 `WithPrompter(nil)` 不清空默认实现）。

---

## 5. 文件布局

| 文件 | 内容 |
|------|------|
| `doc.go` **或**主文件的 package 注释 | 包级文档（一处即可，不强制单独 doc.go） |
| `types.go` | 公开接口（端口）、DTO、Input/Output 类型 |
| `options.go` | `Options`/`Deps` + `Option` + `WithXxx` + 默认常量 |
| `<name>.go` | 核心逻辑，过大时按职责拆分（如 `search.go` / `rerank.go`） |
| `README.md`（可选） | 设计说明与取舍 |

- 提示词子包统一为 `<module>/prompt/`，内含 `types.go`：`//go:embed *.tmpl` 的 `Templates embed.FS`
  + 模板文件名常量（+ 仅在模板变量无法直接复用领域类型时才声明的参数结构）。
  参考 `rag/prompt`、`memory/prompt`。

---

## 6. 提示词内置

- 模块**自带**提示词，不由外部注入：`prompt` 子包 embed 模板，模块内提供默认实现
  （如 `memory.NewBuiltinPrompter`），经 `internal/core/prompt` 渲染；用 `WithPrompter` 可覆盖。
- 模板变量**优先直接复用领域输入类型**，不要另立一套与领域类型字段重复的参数结构。
  `internal/core/prompt` 的渲染器默认启用 sprig 函数（`default`、`join` 等）。

---

## 7. 日志

- core 包**默认不打日志**——日志、指标、追踪是调用方（app 层）的职责。
- 确需日志的模块，注入 `*slog.Logger`（标准库），默认 `slog.Default()`；
  **禁止** import `internal/observability/xlog` 或任何应用日志设施。

---

## 8. 业务无关

- core 不含具体业务字段，也不构造具体数据库查询 DSL。
- 业务元数据通过泛型 `T` + `decoder` 回调带出（`search.Output[T]` / `SourceDecoder[T]`）。
- 业务过滤用中立谓词表达（`vectorstore.Filter` / `Condition`），由适配器翻译到各存储。

---

## 9. 错误

- 用 `internal/xerr` 包装错误（`xerr.Wrap` / `xerr.Wrapf`），保留可读的中文安全信息。

---

## 10. 测试

- 用**实现本模块端口的 fake** 做单测，不依赖真实外部服务（ES、Qdrant、Neo4j、LLM 等）。
- 内置提示词、选项解析、融合/打分等纯逻辑必须有测试覆盖。

---

## 自检清单（提交前）

```bash
# 1. 纯度：core 不得依赖应用基础设施（应为空）
grep -rn 'internal/config"\|internal/repository"\|internal/observability\|internal/svc\|internal/logic"\|internal/prompts"\|internal/models"\|internal/infrastructure\|internal/transport' \
  internal/core --include='*.go' | grep -v '_test.go'

# 2. 无应用日志设施（应为空）
grep -rn 'observability/xlog' internal/core --include='*.go' | grep -v '_test.go'

# 3. 构建 / 测试 / 静态检查
go build ./... && go test ./... && go vet ./...
```
