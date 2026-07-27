package rag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/boxify/api-go/internal/eval"
)

// fakeGenerator 按 query 返回预置答案,并记录收到的上下文条数。
type fakeGenerator struct {
	answers     map[string]string
	err         error
	lastContext []RetrievedHit
}

func (g *fakeGenerator) Generate(_ context.Context, query string, contexts []RetrievedHit) (string, error) {
	g.lastContext = contexts
	if g.err != nil {
		return "", g.err
	}
	return g.answers[query], nil
}

func TestGenerationRunnerPipesContext(t *testing.T) {
	ret := routed{"q": docHits("d1", "d2", "d3")}
	gen := &fakeGenerator{answers: map[string]string{"q": "答案"}}
	r := &GenerationRunner{Retriever: ret, Generator: gen, TopK: 3, MaxContexts: 2}

	rec, err := r.Run(context.Background(), eval.Case{ID: "c", Query: "q"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Answer != "答案" {
		t.Fatalf("answer = %q", rec.Answer)
	}
	if len(gen.lastContext) != 2 {
		t.Fatalf("送入生成的上下文 = %d 条, want 2 (MaxContexts)", len(gen.lastContext))
	}
	if len(rec.Hits) != 3 {
		t.Fatalf("记录里应保留完整检索结果: %d", len(rec.Hits))
	}
	if rec.GenLatency <= 0 {
		t.Fatal("应记录生成耗时")
	}
}

// 生成失败不应中断评测——RAG 挂掉本身就是要被度量的失败。
func TestGenerationRunnerRecordsGenError(t *testing.T) {
	ret := routed{"q": docHits("d1")}
	gen := &fakeGenerator{err: errors.New("model down")}
	rec, err := (&GenerationRunner{Retriever: ret, Generator: gen, TopK: 3}).Run(context.Background(), eval.Case{ID: "c", Query: "q"})
	if err != nil {
		t.Fatalf("生成错误不应作为 Run 的 error 返回: %v", err)
	}
	if rec.GenErr == nil {
		t.Fatal("生成错误应记录在 GenErr")
	}
}

func TestGenerationRunnerNilGuards(t *testing.T) {
	if _, err := (&GenerationRunner{Generator: &fakeGenerator{}}).Run(context.Background(), eval.Case{}); err == nil {
		t.Fatal("nil retriever 应报错")
	}
	if _, err := (&GenerationRunner{Retriever: routed{}}).Run(context.Background(), eval.Case{}); err == nil {
		t.Fatal("nil generator 应报错")
	}
}

func TestContextText(t *testing.T) {
	rec := GenerationRecord{RetrievalRecord: RetrievalRecord{Hits: docHits("d1", "d2")}}
	got := rec.ContextText(1)
	if !strings.Contains(got, "d1") || strings.Contains(got, "d2") {
		t.Fatalf("ContextText(1) 应只含首条: %q", got)
	}
}

func TestFaithfulnessJudge(t *testing.T) {
	ctx := context.Background()
	rec := GenerationRecord{
		RetrievalRecord: RetrievalRecord{Hits: docHits("d1")},
		Answer:          "有据的答案",
	}
	pass := Faithfulness(GenJudge{Client: judgeClient{out: `{"score":0.95,"pass":true,"reason":"全部有据"}`}})
	if s := pass.Score(ctx, eval.Case{Query: "q"}, rec); !s.Passed || s.Value != 0.95 {
		t.Fatalf("faithfulness = %+v", s)
	}
	fail := Faithfulness(GenJudge{Client: judgeClient{out: `{"score":0.2,"pass":false,"reason":"第二段是编造的"}`}})
	if s := fail.Score(ctx, eval.Case{Query: "q"}, rec); s.Passed {
		t.Fatal("裁定不通过时应失败")
	}
}

// 没有检索到任何上下文时,答案无从支撑 —— 必须判 0,而不是让评审"看着办"。
func TestFaithfulnessNoContextFails(t *testing.T) {
	s := Faithfulness(GenJudge{Client: judgeClient{out: `{"score":1,"pass":true}`}}).
		Score(context.Background(), eval.Case{Query: "q"}, GenerationRecord{Answer: "凭空作答"})
	if s.Passed {
		t.Fatal("无上下文应判不通过")
	}
}

func TestFaithfulnessEmptyAnswerFails(t *testing.T) {
	rec := GenerationRecord{RetrievalRecord: RetrievalRecord{Hits: docHits("d1")}}
	if s := (Faithfulness(GenJudge{Client: judgeClient{out: `{"score":1}`}})).Score(context.Background(), eval.Case{}, rec); s.Passed {
		t.Fatal("空答案应判不通过")
	}
}

func TestGenScorersSkipWithoutClient(t *testing.T) {
	rec := GenerationRecord{RetrievalRecord: RetrievalRecord{Hits: docHits("d1")}, Answer: "x"}
	c := eval.Case{Query: "q", Expect: map[string]any{"reference": "参考"}}
	for _, s := range []GenScorer{
		Faithfulness(GenJudge{}), AnswerRelevance(GenJudge{}), AnswerCorrectness(GenJudge{}),
	} {
		if got := s.Score(context.Background(), c, rec); !got.Skipped {
			t.Fatalf("%s 无 client 时应跳过: %+v", s.Name(), got)
		}
	}
}

func TestAnswerCorrectnessSkipsWithoutReference(t *testing.T) {
	rec := GenerationRecord{Answer: "x"}
	s := AnswerCorrectness(GenJudge{Client: judgeClient{out: `{"score":1}`}}).
		Score(context.Background(), eval.Case{Query: "q"}, rec)
	if !s.Skipped {
		t.Fatal("无 reference 应跳过")
	}
}

// 确定性兜底:关键事实用子串断言,不花 LLM 成本。
func TestAnswerContains(t *testing.T) {
	ctx := context.Background()
	rec := GenerationRecord{Answer: "端口是 6334,用 gRPC"}
	c := eval.Case{Expect: map[string]any{"answer_contains": []any{"6334", "gRPC"}}}
	if s := AnswerContains().Score(ctx, c, rec); !s.Passed {
		t.Fatalf("应通过: %+v", s)
	}
	miss := eval.Case{Expect: map[string]any{"answer_contains": "9200"}}
	if s := AnswerContains().Score(ctx, miss, rec); s.Passed {
		t.Fatal("缺失子串应失败")
	}
	if s := AnswerContains().Score(ctx, eval.Case{}, rec); !s.Skipped {
		t.Fatal("无该键应跳过")
	}
}

func TestGenEvaluatorAggregates(t *testing.T) {
	ds := &eval.Dataset{Name: "gen", Cases: []eval.Case{
		{ID: "a", Query: "q1", Expect: map[string]any{"answer_contains": "对"}},
		{ID: "b", Query: "q2", Expect: map[string]any{"answer_contains": "对"}},
	}}
	ret := routed{"q1": docHits("d1"), "q2": docHits("d2")}
	gen := &fakeGenerator{answers: map[string]string{"q1": "对的答案", "q2": "错的答案"}}
	e := &GenEvaluator{
		Runner:  &GenerationRunner{Retriever: ret, Generator: gen, TopK: 3},
		Scorers: []GenScorer{AnswerContains()},
	}
	rep, err := e.Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.PassRate != 0.5 {
		t.Fatalf("pass rate = %v, want 0.5", rep.PassRate)
	}
	if rep.Cases[0].Answer != "对的答案" {
		t.Fatalf("报告应带上答案: %q", rep.Cases[0].Answer)
	}
}

// 明确弃答不作任何断言,不可能产生幻觉 —— 忠实度必须判满分,且不应调用评审模型。
// 此前 rubric 未区分,模型正确回答"根据已有资料无法回答"反被判 0 分。
func TestFaithfulnessTreatsAbstentionAsFaithful(t *testing.T) {
	rec := GenerationRecord{
		RetrievalRecord: RetrievalRecord{Hits: docHits("d1")},
		Answer:          "根据已有资料无法回答。",
	}
	// 评审若被调用会返回不通过;弃答路径应绕过它。
	j := GenJudge{Client: judgeClient{out: `{"score":0,"pass":false,"reason":"上下文没提到"}`}}
	s := Faithfulness(j).Score(context.Background(), eval.Case{Query: "红烧肉炖多久"}, rec)
	if !s.Passed || s.Value != 1 {
		t.Fatalf("弃答应判满分通过: %+v", s)
	}
}

func TestAbstentionMarkersConfigurable(t *testing.T) {
	j := GenJudge{AbstentionMarkers: []string{"NO_ANSWER"}}
	if !j.isAbstention("NO_ANSWER") {
		t.Fatal("自定义标记应生效")
	}
	if j.isAbstention("无法回答") {
		t.Fatal("自定义标记应覆盖默认值")
	}
	if !(GenJudge{}).isAbstention("根据已有资料无法回答") {
		t.Fatal("默认标记应识别常见弃答表述")
	}
}
