package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/boxify/api-go/internal/core/llm"
	"github.com/boxify/api-go/internal/eval"
)

// Generator 用检索到的上下文回答问题(RAG 的生成端)。
//
// 实现可以是直接拼 prompt 调模型,也可以是走 agent + knowledge_search 工具的完整链路。
type Generator interface {
	Generate(ctx context.Context, query string, contexts []RetrievedHit) (string, error)
}

// GenerationRecord 是一次"检索 + 生成"的产物。
//
// 内嵌 RetrievalRecord,使检索侧指标(召回/排序)与生成侧指标(忠实度/正确性)能在
// 同一次运行里一起评——检索没召回到证据时,生成不忠实往往是检索的锅,分开跑就看不出来。
type GenerationRecord struct {
	RetrievalRecord
	Answer     string
	GenLatency time.Duration
	GenErr     error
}

// ContextText 把检索到的前 n 条命中拼成给评审看的上下文文本;n<=0 表示全部。
func (r GenerationRecord) ContextText(n int) string {
	hits := r.Hits
	if n > 0 && len(hits) > n {
		hits = hits[:n]
	}
	var b strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&b, "[%d] %s\n%s\n\n", i+1, h.DocName, h.Content)
	}
	return strings.TrimSpace(b.String())
}

// GenScorer 是生成侧打分器。与检索侧 Scorer 并列,同样产出 eval.Score 以复用报告与门禁。
type GenScorer interface {
	Name() string
	Score(ctx context.Context, c eval.Case, r GenerationRecord) eval.Score
}

// GenerationRunner 先检索、再把上下文交给 Generator 生成答案。
type GenerationRunner struct {
	Retriever Retriever
	Generator Generator
	TopK      int
	// MaxContexts 限制送入生成的命中条数;<=0 表示与 TopK 相同。
	MaxContexts int
}

// Run 执行一次"检索 → 生成"。
//
// 检索或生成的错误落入记录(而非中断评测),使打分器仍能对空答案给出 0 分——RAG 挂掉
// 本身就是要被度量的失败,不该让整个评测崩掉。
func (r *GenerationRunner) Run(ctx context.Context, c eval.Case) (GenerationRecord, error) {
	if r.Retriever == nil {
		return GenerationRecord{}, errors.New("rag: GenerationRunner.Retriever is nil")
	}
	if r.Generator == nil {
		return GenerationRecord{}, errors.New("rag: GenerationRunner.Generator is nil")
	}
	retrieval := &RetrievalRunner{Retriever: r.Retriever, TopK: r.TopK}
	rec, err := retrieval.Run(ctx, c)
	if err != nil {
		return GenerationRecord{}, err
	}
	out := GenerationRecord{RetrievalRecord: rec}

	n := r.MaxContexts
	if n <= 0 {
		n = rec.RequestedK
	}
	contexts := rec.topKHits(n)

	start := time.Now()
	answer, genErr := r.Generator.Generate(ctx, c.Query, contexts)
	out.GenLatency = time.Since(start)
	out.Answer, out.GenErr = answer, genErr
	return out, nil
}

// GenEvaluator 用一组生成侧打分器在数据集上评估"检索 + 生成"。
type GenEvaluator struct {
	Runner  *GenerationRunner
	Scorers []GenScorer
}

// Run 逐用例跑完并聚合为 eval.Report(可复用 Diff/门禁/落盘)。
func (e *GenEvaluator) Run(ctx context.Context, ds *eval.Dataset) (*eval.Report, error) {
	if ds == nil {
		return nil, errors.New("rag: nil dataset")
	}
	if e.Runner == nil {
		return nil, errors.New("rag: nil runner")
	}
	cases := make([]eval.CaseResult, 0, len(ds.Cases))
	for _, c := range ds.Cases {
		record, err := e.Runner.Run(ctx, c)
		if err != nil {
			return nil, err
		}
		cr := eval.CaseResult{
			CaseID:    c.ID,
			Tags:      c.Tags,
			LatencyMS: (record.Latency + record.GenLatency).Milliseconds(),
			Answer:    record.Answer,
		}
		for _, s := range e.Scorers {
			cr.Scores = append(cr.Scores, s.Score(ctx, c, record))
		}
		cases = append(cases, cr)
	}
	return eval.Aggregate(ds.Name, cases), nil
}

// DefaultGenPrompt 是 RAG 生成的默认提示词:强约束"只依据上下文作答"。
//
// 明确要求"上下文没有就说不知道",是为了让 Faithfulness 评的是模型的克制程度,
// 而不是它在没有提示约束时的自由发挥。
const DefaultGenPrompt = `你是严谨的技术问答助手。**只依据下面提供的上下文作答**,
不要引入上下文之外的知识。若上下文不足以回答,直接说"根据已有资料无法回答"。
回答要简洁、直接。

上下文:
%s

问题:%s`

// LLMGenerator 把检索上下文拼进提示词,调用模型生成答案。
//
// Prompt 需含两个 %%s 占位符,依次为上下文与问题;为空时用 DefaultGenPrompt。
type LLMGenerator struct {
	Client llm.Client
	Prompt string
}

// Generate 实现 Generator。
func (g LLMGenerator) Generate(ctx context.Context, query string, contexts []RetrievedHit) (string, error) {
	if g.Client == nil {
		return "", errors.New("rag: LLMGenerator.Client is nil")
	}
	rec := GenerationRecord{RetrievalRecord: RetrievalRecord{Hits: contexts}}
	prompt := g.Prompt
	if prompt == "" {
		prompt = DefaultGenPrompt
	}
	return g.Client.Invoke(ctx, []*llm.Message{{
		Role:    llm.UserRole,
		Content: fmt.Sprintf(prompt, rec.ContextText(0), query),
	}})
}
