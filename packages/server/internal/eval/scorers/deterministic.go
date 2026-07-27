package scorers

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/boxify/api-go/internal/eval"
)

func skipped(name string) eval.Score {
	return eval.Score{Scorer: name, Skipped: true, Passed: true, Detail: "skipped: no expectation"}
}
func passed(name string, detail string) eval.Score {
	return eval.Score{Scorer: name, Value: 1, Passed: true, Detail: detail}
}
func failed(name, detail string) eval.Score {
	return eval.Score{Scorer: name, Value: 0, Passed: false, Detail: detail}
}
func errored(name string, err error) eval.Score {
	return eval.Score{Scorer: name, Passed: false, Err: err, ErrText: err.Error()}
}

// fn 把一个打分函数适配成 eval.Scorer。
type fn struct {
	name string
	f    func(ctx context.Context, c eval.Case, r eval.RunRecord) eval.Score
}

func (s fn) Name() string { return s.name }
func (s fn) Score(ctx context.Context, c eval.Case, r eval.RunRecord) eval.Score {
	return s.f(ctx, c, r)
}

// ExactMatch 断言最终答案与 expect.answer 全等(去首尾空白)。
func ExactMatch() eval.Scorer {
	return fn{"exact_match", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		want, ok := eval.ExpectString(c, "answer")
		if !ok {
			return skipped("exact_match")
		}
		if strings.TrimSpace(r.Answer()) == strings.TrimSpace(want) {
			return passed("exact_match", "")
		}
		return failed("exact_match", fmt.Sprintf("answer %q != %q", r.Answer(), want))
	}}
}

// Contains 断言最终答案包含 expect.contains 的所有子串。
func Contains() eval.Scorer {
	return fn{"contains", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		subs, ok := eval.ExpectStrings(c, "contains")
		if !ok {
			return skipped("contains")
		}
		for _, sub := range subs {
			if !strings.Contains(r.Answer(), sub) {
				return failed("contains", fmt.Sprintf("missing %q", sub))
			}
		}
		return passed("contains", "")
	}}
}

// Regex 断言最终答案匹配 expect.regex。
func Regex() eval.Scorer {
	return fn{"regex", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		pat, ok := eval.ExpectString(c, "regex")
		if !ok {
			return skipped("regex")
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return errored("regex", err)
		}
		if re.MatchString(r.Answer()) {
			return passed("regex", "")
		}
		return failed("regex", "no match")
	}}
}

// JSONValid 断言最终答案是合法 JSON(需 expect.json_valid 触发)。
func JSONValid() eval.Scorer {
	return fn{"json_valid", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		if _, ok := c.Expect["json_valid"]; !ok {
			return skipped("json_valid")
		}
		var v any
		if err := json.Unmarshal([]byte(r.Answer()), &v); err != nil {
			return failed("json_valid", err.Error())
		}
		return passed("json_valid", "")
	}}
}

// ToolTrajectory 断言工具调用轨迹满足 expect.tools。
//
// tools 可为字符串数组(默认 contains 子集模式),或对象 {"mode":"contains|exact","names":[...]}。
func ToolTrajectory() eval.Scorer {
	return fn{"tool_trajectory", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		raw, ok := c.Expect["tools"]
		if !ok {
			return skipped("tool_trajectory")
		}
		mode, names := "contains", []string{}
		switch t := raw.(type) {
		case map[string]any:
			if m, ok := t["mode"].(string); ok {
				mode = m
			}
			if arr, ok := t["names"].([]any); ok {
				for _, e := range arr {
					if s, ok := e.(string); ok {
						names = append(names, s)
					}
				}
			}
		case []any:
			for _, e := range t {
				if s, ok := e.(string); ok {
					names = append(names, s)
				}
			}
		default:
			return errored("tool_trajectory", fmt.Errorf("bad tools expectation"))
		}
		got := r.ToolTrajectory()
		if mode == "exact" {
			if len(got) != len(names) {
				return failed("tool_trajectory", fmt.Sprintf("got %v want exact %v", got, names))
			}
			for i := range names {
				if got[i] != names[i] {
					return failed("tool_trajectory", fmt.Sprintf("got %v want exact %v", got, names))
				}
			}
			return passed("tool_trajectory", "")
		}
		set := map[string]bool{}
		for _, g := range got {
			set[g] = true
		}
		for _, n := range names {
			if !set[n] {
				return failed("tool_trajectory", fmt.Sprintf("missing tool %q in %v", n, got))
			}
		}
		return passed("tool_trajectory", "")
	}}
}

// StopReasonIs 断言停止原因等于 expect.stop_reason。
func StopReasonIs() eval.Scorer {
	return fn{"stop_reason", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		want, ok := eval.ExpectString(c, "stop_reason")
		if !ok {
			return skipped("stop_reason")
		}
		if r.StopReason() == want {
			return passed("stop_reason", "")
		}
		return failed("stop_reason", fmt.Sprintf("stop %q != %q", r.StopReason(), want))
	}}
}

// MaxIterations 断言迭代次数不超过 expect.max_iterations。
func MaxIterations() eval.Scorer {
	return fn{"max_iterations", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		max, ok := eval.ExpectInt(c, "max_iterations")
		if !ok {
			return skipped("max_iterations")
		}
		if r.Iterations() <= max {
			return passed("max_iterations", "")
		}
		return failed("max_iterations", fmt.Sprintf("iters %d > %d", r.Iterations(), max))
	}}
}
