package rag

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/boxify/api-go/internal/eval"
)

// Variant 是一组待对比的检索配置(如不同的向量/BM25 权重、top_k、是否开重排)。
type Variant struct {
	Name   string
	Runner *RetrievalRunner
}

// Comparer 在同一数据集、同一组打分器下横向对比多个检索配置。
//
// 这是 RAG 调参的主用途:回答"向量权重调高有没有提升""开重排值不值""top_k 取多少",
// 用同一批 golden 标注量化各配置差异,而不是凭感觉。
type Comparer struct {
	Variants []Variant
	Scorers  []Scorer
}

// VariantReport 是单个配置的评测结果。
type VariantReport struct {
	Name   string       `json:"name"`
	Report *eval.Report `json:"report"`
}

// Comparison 汇总各配置在同一数据集上的表现。
type Comparison struct {
	Dataset  string          `json:"dataset"`
	Variants []VariantReport `json:"variants"`
}

// Run 依次跑完所有配置。任一配置出错即返回错误(对比结果不完整没有意义)。
func (c *Comparer) Run(ctx context.Context, ds *eval.Dataset) (*Comparison, error) {
	if ds == nil {
		return nil, errors.New("rag: nil dataset")
	}
	if len(c.Variants) == 0 {
		return nil, errors.New("rag: 至少需要一个对比配置")
	}
	out := &Comparison{Dataset: ds.Name}
	for _, v := range c.Variants {
		if v.Runner == nil {
			return nil, fmt.Errorf("rag: 配置 %q 缺少 Runner", v.Name)
		}
		e := &Evaluator{Runner: v.Runner, Scorers: c.Scorers}
		rep, err := e.Run(ctx, ds)
		if err != nil {
			return nil, fmt.Errorf("rag: 配置 %q 评测失败: %w", v.Name, err)
		}
		out.Variants = append(out.Variants, VariantReport{Name: v.Name, Report: rep})
	}
	return out, nil
}

// Report 返回指定配置的报告;不存在时返回 nil。
func (c *Comparison) Report(name string) *eval.Report {
	for _, v := range c.Variants {
		if v.Name == name {
			return v.Report
		}
	}
	return nil
}

// Best 返回指定打分器均值最高的配置名;并列时取先出现者,无数据时返回空串。
func (c *Comparison) Best(scorer string) string {
	best, bestVal, found := "", 0.0, false
	for _, v := range c.Variants {
		agg, ok := v.Report.Scorers[scorer]
		if !ok {
			continue
		}
		if !found || agg.MeanValue > bestVal {
			best, bestVal, found = v.Name, agg.MeanValue, true
		}
	}
	return best
}

// ScorerDelta 是某打分器上 candidate 相对 base 的均值差。
type ScorerDelta struct {
	Scorer    string  `json:"scorer"`
	Base      float64 `json:"base"`
	Candidate float64 `json:"candidate"`
	Delta     float64 `json:"delta"`
}

// Delta 给出 candidate 相对 base 的逐打分器均值差(正数=candidate 更好)。
//
// 任一配置不存在时返回 nil。结果按打分器名排序,便于稳定输出与快照比对。
func (c *Comparison) Delta(base, candidate string) []ScorerDelta {
	baseRep, candRep := c.Report(base), c.Report(candidate)
	if baseRep == nil || candRep == nil {
		return nil
	}
	names := scorerNames(baseRep, candRep)
	out := make([]ScorerDelta, 0, len(names))
	for _, n := range names {
		b := baseRep.Scorers[n].MeanValue
		cand := candRep.Scorers[n].MeanValue
		out = append(out, ScorerDelta{Scorer: n, Base: b, Candidate: cand, Delta: cand - b})
	}
	return out
}

// WriteTable 输出各配置 × 各打分器均值的对比表,末列为 pass rate。
func (c *Comparison) WriteTable(w io.Writer) error {
	if len(c.Variants) == 0 {
		return nil
	}
	reps := make([]*eval.Report, 0, len(c.Variants))
	for _, v := range c.Variants {
		reps = append(reps, v.Report)
	}
	names := scorerNames(reps...)

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "VARIANT")
	for _, n := range names {
		fmt.Fprintf(tw, "\t%s", n)
	}
	fmt.Fprintf(tw, "\tpass_rate\n")
	for _, v := range c.Variants {
		fmt.Fprintf(tw, "%s", v.Name)
		for _, n := range names {
			if agg, ok := v.Report.Scorers[n]; ok {
				fmt.Fprintf(tw, "\t%.4f", agg.MeanValue)
			} else {
				fmt.Fprintf(tw, "\t-")
			}
		}
		fmt.Fprintf(tw, "\t%.2f%%\n", v.Report.PassRate*100)
	}
	return tw.Flush()
}

// scorerNames 收集若干报告中出现过的全部打分器名,按字典序排序。
func scorerNames(reps ...*eval.Report) []string {
	set := map[string]bool{}
	for _, rep := range reps {
		if rep == nil {
			continue
		}
		for n := range rep.Scorers {
			set[n] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
