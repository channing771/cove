package eval

import (
	"math/rand"
	"sort"
)

// Interval 是一个闭区间估计。
type Interval struct {
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
}

// Contains 判断区间是否包含某值。
func (i Interval) Contains(v float64) bool { return v >= i.Lo && v <= i.Hi }

// ZeroEpsilon 是判定"区间确实不含 0"的数值容差。
//
// 离散指标(Recall、MRR 等取值是有限个分数)加小样本时,自助区间的边界经常**恰好**落在
// 0 上;此时浮点求和的残差(量级 1e-17)会让 Lo>0 成立,把"边界贴着 0"误判成显著差异。
const ZeroEpsilon = 1e-9

// ExcludesZero 判断区间是否在容差之外完整位于 0 的一侧。
//
// 这是判定差异显著性的正确谓词:区间紧贴 0(上界或下界为 0)意味着"无差异"仍在置信范围内,
// 不能声称显著。
func (i Interval) ExcludesZero() bool { return i.Lo > ZeroEpsilon || i.Hi < -ZeroEpsilon }

// DefaultBootstrapSamples 是重采样次数的默认值。
const DefaultBootstrapSamples = 2000

// BootstrapMeanCI 用自助重采样估计均值的置信区间。
//
// 评测数据集通常只有几十条用例,单看均值无法区分"真实提升"与"抽样噪声"——例如 12 条
// 用例下,一条用例的排名变化就能让均值波动数个百分点。给出区间后,只有当两个配置的
// 差值区间不跨 0 时,才谈得上"确实更好"。
//
// seed 固定以保证可复现(基线要能重复比对);level 为置信水平(如 0.95)。
// 样本为空时返回 ok=false。
func BootstrapMeanCI(values []float64, level float64, samples int, seed int64) (Interval, bool) {
	n := len(values)
	if n == 0 {
		return Interval{}, false
	}
	if samples <= 0 {
		samples = DefaultBootstrapSamples
	}
	if level <= 0 || level >= 1 {
		level = 0.95
	}
	rng := rand.New(rand.NewSource(seed))
	means := make([]float64, samples)
	for s := 0; s < samples; s++ {
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += values[rng.Intn(n)]
		}
		means[s] = sum / float64(n)
	}
	sort.Float64s(means)
	alpha := (1 - level) / 2
	lo := means[clampIndex(int(alpha*float64(samples)), samples)]
	hi := means[clampIndex(int((1-alpha)*float64(samples))-1, samples)]
	return Interval{Lo: lo, Hi: hi}, true
}

func clampIndex(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// ScorerValues 返回某打分器在各用例上的取值(跳过的用例不计入)。
func (r *Report) ScorerValues(scorer string) []float64 {
	var out []float64
	for _, c := range r.Cases {
		for _, s := range c.Scores {
			if s.Scorer == scorer && !s.Skipped {
				out = append(out, s.Value)
			}
		}
	}
	return out
}

// MeanCI 返回某打分器均值的自助置信区间;无样本时 ok=false。
func (r *Report) MeanCI(scorer string, level float64, samples int, seed int64) (Interval, bool) {
	return BootstrapMeanCI(r.ScorerValues(scorer), level, samples, seed)
}

// CaseValues 返回某打分器按 case_id 索引的取值(跳过的不计入),用于配对比较。
func (r *Report) CaseValues(scorer string) map[string]float64 {
	out := map[string]float64{}
	for _, c := range r.Cases {
		for _, s := range c.Scores {
			if s.Scorer == scorer && !s.Skipped {
				out[c.CaseID] = s.Value
			}
		}
	}
	return out
}
