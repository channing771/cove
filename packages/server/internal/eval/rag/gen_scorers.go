package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/boxify/api-go/internal/core/llm"
	"github.com/boxify/api-go/internal/eval"
)

// GenJudge 是生成侧 LLM 评审的公共实现:按 rubric 拼 prompt、调模型、解析结构化裁定。
//
// Client 为 nil 时跳过(CI 默认不注入,避免联网与成本);PassThreshold 为 0 时取 0.5。
type GenJudge struct {
	Client        llm.Client
	PassThreshold float64
	// AbstentionMarkers 用于识别"明确表示资料不足、拒绝作答"的回答;为空时用
	// DefaultAbstentionMarkers。
	AbstentionMarkers []string
}

// DefaultAbstentionMarkers 是默认的弃答标记。
var DefaultAbstentionMarkers = []string{"无法回答", "无法作答", "资料不足", "没有相关信息", "未提及", "无法根据"}

// isAbstention 判断回答是否为明确弃答。
//
// 弃答**不作任何断言**,因此不可能产生幻觉——忠实度必须判满分。此前 rubric 未区分这一点,
// 模型正确回答"根据已有资料无法回答"反被判 0 分,等于把调优方向推向编造答案。
func (j GenJudge) isAbstention(answer string) bool {
	markers := j.AbstentionMarkers
	if len(markers) == 0 {
		markers = DefaultAbstentionMarkers
	}
	for _, m := range markers {
		if strings.Contains(answer, m) {
			return true
		}
	}
	return false
}

type genVerdict struct {
	Score  float64 `json:"score"`
	Pass   *bool   `json:"pass"`
	Reason string  `json:"reason"`
}

// judge 用给定 prompt 调评审模型并解析裁定。
func (j GenJudge) judge(ctx context.Context, name, prompt string) eval.Score {
	if j.Client == nil {
		return eval.Score{Scorer: name, Skipped: true, Passed: true, Detail: "skipped: no judge client"}
	}
	out, err := j.Client.Invoke(ctx, []*llm.Message{{Role: llm.UserRole, Content: prompt}})
	if err != nil {
		return errScore(name, err)
	}
	obj, ok := eval.FirstJSONObject(out)
	if !ok {
		return errScore(name, fmt.Errorf("裁定中没有 JSON 对象: %q", truncate(out, 200)))
	}
	var v genVerdict
	if err := json.Unmarshal([]byte(obj), &v); err != nil {
		return errScore(name, fmt.Errorf("解析裁定失败: %w", err))
	}
	threshold := j.PassThreshold
	if threshold == 0 {
		threshold = 0.5
	}
	passed := v.Score >= threshold
	if v.Pass != nil {
		passed = *v.Pass
	}
	return eval.Score{Scorer: name, Value: v.Score, Passed: passed, Detail: v.Reason}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

const faithfulnessRubric = `你是严格的事实核查员。判断"回答"中的每个论断是否**都能在给定上下文中找到支撑**。
只要有任何一处上下文未提及却被断言的内容(即幻觉),就应判不通过。上下文没提到的常识也算不通过。
**特别注意**:若回答只是表明资料不足、无法作答(未作任何事实断言),不存在幻觉,应判满分通过。
只输出一个 JSON 对象:{"score": 0到1的小数, "pass": true或false, "reason": "指出未被支撑的具体内容,或说明全部有据"}

问题:%s

上下文:
%s

回答:
%s`

// Faithfulness 评判答案是否**完全由检索上下文支撑**(有无幻觉)。
//
// 这是 RAG 最关键的生成侧指标:检索得再准,若模型编造上下文里没有的内容,答案依然不可用。
// 上下文为空或答案为空时直接判 0(无从支撑)。
func Faithfulness(j GenJudge) GenScorer {
	return genFn{"faithfulness", func(ctx context.Context, c eval.Case, r GenerationRecord) eval.Score {
		if j.Client == nil {
			return eval.Score{Scorer: "faithfulness", Skipped: true, Passed: true, Detail: "skipped: no judge client"}
		}
		contexts := r.ContextText(0)
		if strings.TrimSpace(contexts) == "" {
			return badScore("faithfulness", "无检索上下文,答案无从支撑")
		}
		if strings.TrimSpace(r.Answer) == "" {
			return badScore("faithfulness", "空答案")
		}
		// 弃答不作任何断言 → 不存在幻觉,判满分(不必花钱问评审)。
		if j.isAbstention(r.Answer) {
			return okScore("faithfulness", "明确弃答,未作任何断言,无幻觉风险")
		}
		return j.judge(ctx, "faithfulness", fmt.Sprintf(faithfulnessRubric, c.Query, contexts, r.Answer))
	}}
}

const answerRelevanceRubric = `你是严格的评审。判断"回答"是否切题地回应了"问题"(不看事实对错,只看是否答到点上)。
答非所问、答了但没回应核心诉求、大量无关内容,都应扣分。
**特别注意**:若上下文中确实不含回答该问题所需的信息,而回答明确说明"资料不足/无法作答",
这是**恰当的回应**,应判满分通过;不要因为"没给出具体答案"而扣分。
只输出一个 JSON 对象:{"score": 0到1的小数, "pass": true或false, "reason": "简短理由"}

问题:%s

上下文:
%s

回答:
%s`

// AnswerRelevance 评判答案是否切题回应了问题(与事实正确性正交)。
func AnswerRelevance(j GenJudge) GenScorer {
	return genFn{"answer_relevance", func(ctx context.Context, c eval.Case, r GenerationRecord) eval.Score {
		if j.Client == nil {
			return eval.Score{Scorer: "answer_relevance", Skipped: true, Passed: true, Detail: "skipped: no judge client"}
		}
		if strings.TrimSpace(r.Answer) == "" {
			return badScore("answer_relevance", "空答案")
		}
		return j.judge(ctx, "answer_relevance", fmt.Sprintf(answerRelevanceRubric, c.Query, r.ContextText(0), r.Answer))
	}}
}

const answerCorrectnessRubric = `你是严格的评审。对照"参考答案"判断"回答"是否正确、完整。
表述可以不同,但关键事实必须一致;遗漏关键点或与参考答案冲突都应扣分。
只输出一个 JSON 对象:{"score": 0到1的小数, "pass": true或false, "reason": "简短理由"}

问题:%s

参考答案:
%s

回答:
%s`

// AnswerCorrectness 对照 expect.reference 评判答案正确性;无参考答案时跳过。
func AnswerCorrectness(j GenJudge) GenScorer {
	return genFn{"answer_correctness", func(ctx context.Context, c eval.Case, r GenerationRecord) eval.Score {
		reference, ok := eval.ExpectString(c, "reference")
		if !ok || strings.TrimSpace(reference) == "" {
			return skip("answer_correctness")
		}
		if j.Client == nil {
			return eval.Score{Scorer: "answer_correctness", Skipped: true, Passed: true, Detail: "skipped: no judge client"}
		}
		if strings.TrimSpace(r.Answer) == "" {
			return badScore("answer_correctness", "空答案")
		}
		return j.judge(ctx, "answer_correctness", fmt.Sprintf(answerCorrectnessRubric, c.Query, reference, r.Answer))
	}}
}

// AnswerContains 是确定性兜底:断言答案包含 expect.answer_contains 的全部子串。
//
// 无需 LLM、无成本,适合关键事实的硬门禁(如版本号、端口、命令名)。
func AnswerContains() GenScorer {
	return genFn{"answer_contains", func(_ context.Context, c eval.Case, r GenerationRecord) eval.Score {
		subs, ok := eval.ExpectStrings(c, "answer_contains")
		if !ok {
			return skip("answer_contains")
		}
		for _, sub := range subs {
			if !strings.Contains(r.Answer, sub) {
				return badScore("answer_contains", fmt.Sprintf("答案缺少 %q", sub))
			}
		}
		return okScore("answer_contains", "")
	}}
}

// genFn 把打分函数适配成 GenScorer。
type genFn struct {
	name string
	f    func(ctx context.Context, c eval.Case, r GenerationRecord) eval.Score
}

func (s genFn) Name() string { return s.name }
func (s genFn) Score(ctx context.Context, c eval.Case, r GenerationRecord) eval.Score {
	return s.f(ctx, c, r)
}
