package scorers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/boxify/api-go/internal/core/llm"
	"github.com/boxify/api-go/internal/eval"
)

const defaultRubric = `你是严格的评审。根据问题与回答质量打分,只输出一个 JSON 对象:
{"score": 0到1的小数, "pass": true或false, "reason": "简短理由"}

问题:{{.Query}}
回答:{{.Answer}}
{{if .Reference}}参考答案:{{.Reference}}{{end}}`

// LLMJudge 用模型按 rubric 对回答打分。
//
// Rubric 为 text/template(可用 .Query/.Answer/.Reference),为空用内置默认。
// PassThreshold 为 0 时取 0.5。Client 为 nil 时跳过(CI 默认不注入)。
type LLMJudge struct {
	Client        llm.Client
	Rubric        string
	PassThreshold float64
}

// Name 返回打分器名。
func (j LLMJudge) Name() string { return "llm_judge" }

type judgeVerdict struct {
	Score  float64 `json:"score"`
	Pass   *bool   `json:"pass"`
	Reason string  `json:"reason"`
}

// Score 调用评审模型并解析结构化裁定。
func (j LLMJudge) Score(ctx context.Context, c eval.Case, r eval.RunRecord) eval.Score {
	if j.Client == nil {
		return eval.Score{Scorer: "llm_judge", Skipped: true, Passed: true, Detail: "skipped: no judge client"}
	}
	rubric := j.Rubric
	if rubric == "" {
		rubric = defaultRubric
	}
	tmpl, err := template.New("rubric").Parse(rubric)
	if err != nil {
		return errored("llm_judge", err)
	}
	ref, _ := eval.ExpectString(c, "reference")
	var sb strings.Builder
	if err := tmpl.Execute(&sb, map[string]any{"Query": c.Query, "Answer": r.Answer(), "Reference": ref}); err != nil {
		return errored("llm_judge", err)
	}
	out, err := j.Client.Invoke(ctx, []*llm.Message{{Role: llm.UserRole, Content: sb.String()}})
	if err != nil {
		return errored("llm_judge", err)
	}
	v, err := parseVerdict(out)
	if err != nil {
		return errored("llm_judge", err)
	}
	threshold := j.PassThreshold
	if threshold == 0 {
		threshold = 0.5
	}
	passed := v.Score >= threshold
	if v.Pass != nil {
		passed = *v.Pass
	}
	return eval.Score{Scorer: "llm_judge", Value: v.Score, Passed: passed, Detail: v.Reason}
}

// parseVerdict 从模型输出中抽取首个 JSON 对象并解析裁定。
func parseVerdict(out string) (judgeVerdict, error) {
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end < start {
		return judgeVerdict{}, fmt.Errorf("no json object in judge output: %q", out)
	}
	var v judgeVerdict
	if err := json.Unmarshal([]byte(out[start:end+1]), &v); err != nil {
		return judgeVerdict{}, fmt.Errorf("parse judge verdict: %w", err)
	}
	return v, nil
}
