package eval

import (
	"context"
	"strings"
)

// Score 表示单个打分器对一条用例的评分结果。
//
// Value 归一化到 0..1。Skipped 表示该用例缺少对应期望而跳过(不计通过/失败)。
// Err 非 nil 表示打分过程本身出错,该用例应计失败;ErrText 是其可序列化镜像。
type Score struct {
	Scorer  string  `json:"scorer"`
	Value   float64 `json:"value"`
	Passed  bool    `json:"passed"`
	Skipped bool    `json:"skipped,omitempty"`
	Detail  string  `json:"detail,omitempty"`
	ErrText string  `json:"error,omitempty"`
	Err     error   `json:"-"`
}

// Scorer 是无状态、可组合的打分器。一期(离线)与二期(在线)共用本接口。
type Scorer interface {
	Name() string
	Score(ctx context.Context, c Case, r RunRecord) Score
}

// ExpectString 读取 Expect[key] 的字符串值。
func ExpectString(c Case, key string) (string, bool) {
	v, ok := c.Expect[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// ExpectStrings 读取 Expect[key],支持单个字符串或字符串数组。
func ExpectStrings(c Case, key string) ([]string, bool) {
	v, ok := c.Expect[key]
	if !ok {
		return nil, false
	}
	switch t := v.(type) {
	case string:
		return []string{t}, true
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	case []string:
		return t, true
	}
	return nil, false
}

// ExpectFloat 读取 Expect[key] 的数值(JSON number 反序列化为 float64)。
func ExpectFloat(c Case, key string) (float64, bool) {
	v, ok := c.Expect[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	}
	return 0, false
}

// ExpectInt 读取 Expect[key] 的整数值。
func ExpectInt(c Case, key string) (int, bool) {
	f, ok := ExpectFloat(c, key)
	return int(f), ok
}

// FirstJSONObject 返回 s 中首个大括号平衡的 JSON 对象子串。
//
// 相比"首个 { 到末个 }",本函数正确跳过字符串字面量内的括号与转义,并在深度归零处收尾,
// 避免把目标 JSON 之后的散乱大括号(如解释性文字)一并吞入。供各 LLM-judge 解析裁定复用。
func FirstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}
