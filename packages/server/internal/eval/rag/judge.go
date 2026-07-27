package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/boxify/api-go/internal/core/llm"
	"github.com/boxify/api-go/internal/eval"
)

const defaultRelevanceRubric = `你是严格的检索质量评审。判断"检索到的上下文"整体对回答"问题"的相关/有用程度,
只输出一个 JSON 对象:{"score": 0到1的小数, "pass": true或false, "reason": "简短理由"}

问题:{{QUERY}}

检索到的上下文:
{{CONTEXT}}`

// ContextRelevance 用模型评判检索到的上下文对 query 的整体相关度(无需 golden 标注)。
//
// Client 为 nil 时跳过(CI 默认不注入)。MaxChunks<=0 时默认取前 5 条命中拼接上下文。
// PassThreshold 为 0 时取 0.5。
type ContextRelevance struct {
	Client        llm.Client
	Rubric        string
	MaxChunks     int
	PassThreshold float64
}

// Name 返回打分器名。
func (j ContextRelevance) Name() string { return "context_relevance" }

type relevanceVerdict struct {
	Score  float64 `json:"score"`
	Pass   *bool   `json:"pass"`
	Reason string  `json:"reason"`
}

// Score 拼接检索上下文并调用评审模型解析裁定。
func (j ContextRelevance) Score(ctx context.Context, c eval.Case, r RetrievalRecord) eval.Score {
	if j.Client == nil {
		return eval.Score{Scorer: "context_relevance", Skipped: true, Passed: true, Detail: "skipped: no judge client"}
	}
	max := j.MaxChunks
	if max <= 0 {
		max = 5
	}
	var b strings.Builder
	for i, h := range r.topKHits(max) {
		fmt.Fprintf(&b, "%d. %s\n%s\n\n", i+1, h.DocName, h.Content)
	}
	context := strings.TrimSpace(b.String())
	if context == "" {
		return eval.Score{Scorer: "context_relevance", Value: 0, Passed: false, Detail: "no context retrieved"}
	}
	rubric := j.Rubric
	if rubric == "" {
		rubric = defaultRelevanceRubric
	}
	prompt := strings.ReplaceAll(rubric, "{{QUERY}}", c.Query)
	prompt = strings.ReplaceAll(prompt, "{{CONTEXT}}", context)

	out, err := j.Client.Invoke(ctx, []*llm.Message{{Role: llm.UserRole, Content: prompt}})
	if err != nil {
		return eval.Score{Scorer: "context_relevance", Err: err, ErrText: err.Error()}
	}
	obj, ok := eval.FirstJSONObject(out)
	if !ok {
		err := fmt.Errorf("no json object in judge output: %q", out)
		return eval.Score{Scorer: "context_relevance", Err: err, ErrText: err.Error()}
	}
	var v relevanceVerdict
	if err := json.Unmarshal([]byte(obj), &v); err != nil {
		return eval.Score{Scorer: "context_relevance", Err: err, ErrText: err.Error()}
	}
	threshold := j.PassThreshold
	if threshold == 0 {
		threshold = 0.5
	}
	passed := v.Score >= threshold
	if v.Pass != nil {
		passed = *v.Pass
	}
	return eval.Score{Scorer: "context_relevance", Value: v.Score, Passed: passed, Detail: v.Reason}
}
