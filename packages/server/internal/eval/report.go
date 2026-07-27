package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// CaseResult 表示一条用例的评估结果(可序列化投影,不含 *react.Result)。
type CaseResult struct {
	CaseID      string   `json:"case_id"`
	Tags        []string `json:"tags,omitempty"`
	Passed      bool     `json:"passed"`
	Scores      []Score  `json:"scores"`
	LatencyMS   int64    `json:"latency_ms"`
	Iterations  int      `json:"iterations"`
	TotalTokens int64    `json:"total_tokens"`
	CostUSD     float64  `json:"cost_usd"`
	StopReason  string   `json:"stop_reason"`
	Answer      string   `json:"answer,omitempty"`
}

// ScorerAgg 是单个打分器在数据集上的聚合。
type ScorerAgg struct {
	Scorer    string  `json:"scorer"`
	Runs      int     `json:"runs"`
	Passed    int     `json:"passed"`
	Failed    int     `json:"failed"`
	Skipped   int     `json:"skipped"`
	Errored   int     `json:"errored"`
	MeanValue float64 `json:"mean_value"`
}

// Report 是一次评估的完整结果。
type Report struct {
	Dataset  string               `json:"dataset"`
	Cases    []CaseResult         `json:"cases"`
	PassRate float64              `json:"pass_rate"`
	Scorers  map[string]ScorerAgg `json:"scorers"`
}

// Aggregate 从各用例已填充的 Scores 计算通过态与聚合,产出完整 Report。
//
// 供任意 Runner/记录类型的评估器复用(agent 评测与 RAG 评测共用):调用方只需把每条用例
// 打好分的 CaseResult(Scores 已填,摘要字段按需填)传入,由本函数统一判定 cr.Passed、
// 每 scorer 的通过/失败/跳过/错误计数与均值、以及整体 pass rate。
//
// 判定约定:一条用例内任一 Score 出错或失败 → 该用例失败;跳过不影响通过态,也不计入均值。
func Aggregate(dataset string, cases []CaseResult) *Report {
	rep := &Report{Dataset: dataset, Scorers: map[string]ScorerAgg{}}
	valueSum := map[string]float64{}
	valueCount := map[string]int{}
	for _, cr := range cases {
		casePassed := true
		for i := range cr.Scores {
			s := &cr.Scores[i]
			if s.Err != nil && s.ErrText == "" {
				s.ErrText = s.Err.Error()
			}
			agg := rep.Scorers[s.Scorer]
			agg.Scorer = s.Scorer
			agg.Runs++
			switch {
			case s.Err != nil:
				agg.Errored++
				casePassed = false
			case s.Skipped:
				agg.Skipped++
			case s.Passed:
				agg.Passed++
				valueSum[s.Scorer] += s.Value
				valueCount[s.Scorer]++
			default:
				agg.Failed++
				casePassed = false
				valueSum[s.Scorer] += s.Value
				valueCount[s.Scorer]++
			}
			rep.Scorers[s.Scorer] = agg
		}
		cr.Passed = casePassed
		rep.Cases = append(rep.Cases, cr)
	}
	for name, agg := range rep.Scorers {
		if valueCount[name] > 0 {
			agg.MeanValue = valueSum[name] / float64(valueCount[name])
			rep.Scorers[name] = agg
		}
	}
	if len(rep.Cases) > 0 {
		passed := 0
		for _, cr := range rep.Cases {
			if cr.Passed {
				passed++
			}
		}
		rep.PassRate = float64(passed) / float64(len(rep.Cases))
	}
	return rep
}

// WriteJSON 以缩进 JSON 写出报告。
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteTable 以人读表格写出报告:每行一条用例,列出通过态与各打分器状态。
func (r *Report) WriteTable(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "CASE\tPASSED\tSCORES\n")
	for _, c := range r.Cases {
		status := ""
		for i, s := range c.Scores {
			if i > 0 {
				status += " "
			}
			switch {
			case s.Err != nil || s.ErrText != "":
				status += s.Scorer + "=ERR"
			case s.Skipped:
				status += s.Scorer + "=SKIP"
			case s.Passed:
				status += s.Scorer + "=PASS"
			default:
				status += s.Scorer + "=FAIL"
			}
		}
		fmt.Fprintf(tw, "%s\t%v\t%s\n", c.CaseID, c.Passed, status)
	}
	fmt.Fprintf(tw, "---\tpass_rate\t%.2f%%\n", r.PassRate*100)
	return tw.Flush()
}

// LoadReport 从 JSON 文件载入基线报告。
func LoadReport(path string) (*Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("parse report: %w", err)
	}
	return &rep, nil
}

// Regression 表示一条相对基线新出现的打分失败。
type Regression struct {
	CaseID string `json:"case_id"`
	Scorer string `json:"scorer"`
	Detail string `json:"detail"`
}

// RegressionReport 汇总相对基线的新失败。
type RegressionReport struct {
	NewFailures []Regression `json:"new_failures"`
}

// Diff 对比基线,找出基线通过而当前失败(非跳过)的打分。
func (r *Report) Diff(baseline *Report) *RegressionReport {
	baseScore := map[string]bool{} // key: caseID|scorer -> 基线是否通过(非跳过)
	for _, c := range baseline.Cases {
		for _, s := range c.Scores {
			baseScore[c.CaseID+"|"+s.Scorer] = s.Passed && !s.Skipped
		}
	}
	out := &RegressionReport{}
	for _, c := range r.Cases {
		for _, s := range c.Scores {
			if s.Skipped {
				continue
			}
			wasPass, known := baseScore[c.CaseID+"|"+s.Scorer]
			if known && wasPass && !s.Passed {
				out.NewFailures = append(out.NewFailures, Regression{CaseID: c.CaseID, Scorer: s.Scorer, Detail: s.Detail})
			}
		}
	}
	return out
}

// Failed 表示存在相对基线的新失败。
func (rr *RegressionReport) Failed() bool { return rr != nil && len(rr.NewFailures) > 0 }

// Err 汇总所有新失败为一个错误;无回归时返回 nil。
//
// 供 CI 门禁使用:if err := current.Diff(baseline).Err(); err != nil { t.Fatal(err) }。
// 报告层不引入 testing 依赖,由调用方决定如何失败(t.Fatal/日志/退出码)。
func (rr *RegressionReport) Err() error {
	if !rr.Failed() {
		return nil
	}
	var b strings.Builder
	for i, f := range rr.NewFailures {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "regression: case %q scorer %q now fails (%s)", f.CaseID, f.Scorer, f.Detail)
	}
	return errors.New(b.String())
}
