package scorers

import (
	"context"
	"testing"

	corereact "github.com/boxify/api-go/internal/core/agent/react"
	"github.com/boxify/api-go/internal/eval"
)

// 裁定 JSON 之后跟散乱大括号的解释文字,不应被吞入解析区间。
func TestLLMJudgeIgnoresTrailingBraces(t *testing.T) {
	j := LLMJudge{Client: judgeClient{out: `{"score":0.8,"pass":true,"reason":"ok"} 说明:见 {备注} 段落 }`}}
	s := j.Score(context.Background(), eval.Case{}, eval.RunRecord{Result: &corereact.Result{Answer: "x"}})
	if s.Err != nil {
		t.Fatalf("should parse first balanced object: %v", s.Err)
	}
	if !s.Passed || s.Value != 0.8 {
		t.Fatalf("verdict = %+v", s)
	}
}

// 字符串字面量内含大括号,不影响平衡计数。
func TestFirstJSONObjectHandlesBracesInString(t *testing.T) {
	obj, ok := firstJSONObject(`前言 {"reason":"包含 } 和 { 的文本","score":1} 尾巴`)
	if !ok || obj != `{"reason":"包含 } 和 { 的文本","score":1}` {
		t.Fatalf("obj = %q ok=%v", obj, ok)
	}
}

func TestFirstJSONObjectNone(t *testing.T) {
	if _, ok := firstJSONObject("no braces here"); ok {
		t.Fatal("want false when no object")
	}
}
