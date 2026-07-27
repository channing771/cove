package rag

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/boxify/api-go/internal/eval"
)

// SignificantDelta 是 candidate 相对 base 的均值差及其置信区间。
//
// Significant 为 true 表示差值的自助置信区间不跨 0——只有此时才谈得上"确实更好/更差"。
// 评测数据集常只有几十条用例,单看均值差极易把抽样噪声当成提升。
type SignificantDelta struct {
	Scorer      string        `json:"scorer"`
	Base        float64       `json:"base"`
	Candidate   float64       `json:"candidate"`
	Delta       float64       `json:"delta"`
	CI          eval.Interval `json:"ci"`
	Significant bool          `json:"significant"`
	N           int           `json:"n"` // 参与配对的用例数
}

// DeltaWithCI 给出 candidate 相对 base 的逐打分器差值及其配对自助置信区间。
//
// 配对(而非各自独立抽样)是因为两个配置跑的是同一批用例:同一用例的难度是共同因素,
// 配对后能消掉用例间差异,只留下配置差异,统计效力更高。
// 任一配置不存在时返回 nil;seed 固定以保证可复现。
func (c *Comparison) DeltaWithCI(base, candidate string, level float64, samples int, seed int64) []SignificantDelta {
	baseRep, candRep := c.Report(base), c.Report(candidate)
	if baseRep == nil || candRep == nil {
		return nil
	}
	if samples <= 0 {
		samples = eval.DefaultBootstrapSamples
	}
	if level <= 0 || level >= 1 {
		level = 0.95
	}
	names := scorerNames(baseRep, candRep)
	out := make([]SignificantDelta, 0, len(names))
	for i, name := range names {
		baseVals, candVals := baseRep.CaseValues(name), candRep.CaseValues(name)
		// 只保留两侧都有值的用例(配对)。
		ids := make([]string, 0, len(baseVals))
		for id := range baseVals {
			if _, ok := candVals[id]; ok {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids) // 顺序固定,保证重采样可复现
		diffs := make([]float64, 0, len(ids))
		for _, id := range ids {
			diffs = append(diffs, candVals[id]-baseVals[id])
		}
		d := SignificantDelta{
			Scorer:    name,
			Base:      baseRep.Scorers[name].MeanValue,
			Candidate: candRep.Scorers[name].MeanValue,
			N:         len(diffs),
		}
		d.Delta = mean(diffs)
		// 每个打分器用不同但确定的 seed,避免各指标共用同一组重采样下标。
		if ci, ok := eval.BootstrapMeanCI(diffs, level, samples, seed+int64(i)); ok {
			d.CI = ci
			d.Significant = ci.ExcludesZero()
		}
		out = append(out, d)
	}
	return out
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	return sum / float64(len(v))
}

// WriteDeltaTable 输出带置信区间与显著性标记的差值表。
//
// 只有标注"显著"的行才足以支撑调参决策;其余行的差异在当前样本量下与噪声无法区分。
func (c *Comparison) WriteDeltaTable(w io.Writer, base, candidate string, level float64, samples int, seed int64) error {
	deltas := c.DeltaWithCI(base, candidate, level, samples, seed)
	if deltas == nil {
		return fmt.Errorf("rag: 未知对比配置 %q 或 %q", base, candidate)
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "SCORER\t%s\t%s\tdelta\t%.0f%%CI\t显著\tN\n", base, candidate, level*100)
	for _, d := range deltas {
		mark := "-"
		if d.Significant {
			mark = "是"
		}
		fmt.Fprintf(tw, "%s\t%.4f\t%.4f\t%+.4f\t[%+.4f, %+.4f]\t%s\t%d\n",
			d.Scorer, d.Base, d.Candidate, d.Delta, d.CI.Lo, d.CI.Hi, mark, d.N)
	}
	return tw.Flush()
}
