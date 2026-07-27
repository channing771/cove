package eval

import "testing"

func TestBootstrapMeanCIConstant(t *testing.T) {
	ci, ok := BootstrapMeanCI([]float64{0.7, 0.7, 0.7, 0.7}, 0.95, 500, 1)
	if !ok {
		t.Fatal("常量样本应可计算")
	}
	if !near(ci.Lo, 0.7) || !near(ci.Hi, 0.7) {
		t.Fatalf("常量样本的 CI 应退化为点: %+v", ci)
	}
}

func TestBootstrapMeanCIBracketsMean(t *testing.T) {
	vals := []float64{0.2, 0.4, 0.6, 0.8, 1.0, 0.5, 0.3, 0.9}
	ci, ok := BootstrapMeanCI(vals, 0.95, 2000, 7)
	if !ok {
		t.Fatal("应可计算")
	}
	mean := 0.0
	for _, v := range vals {
		mean += v
	}
	mean /= float64(len(vals))
	if ci.Lo > mean || ci.Hi < mean {
		t.Fatalf("CI %+v 应包含均值 %.4f", ci, mean)
	}
	if ci.Hi <= ci.Lo {
		t.Fatalf("离散样本的 CI 应有宽度: %+v", ci)
	}
}

// 同一 seed 必须得到同一区间,否则基线无法复现。
func TestBootstrapMeanCIDeterministic(t *testing.T) {
	vals := []float64{0.1, 0.9, 0.5, 0.3}
	a, _ := BootstrapMeanCI(vals, 0.95, 1000, 42)
	b, _ := BootstrapMeanCI(vals, 0.95, 1000, 42)
	if a != b {
		t.Fatalf("同 seed 结果应一致: %+v vs %+v", a, b)
	}
	c, _ := BootstrapMeanCI(vals, 0.95, 1000, 43)
	if a == c {
		t.Fatal("不同 seed 应给出不同重采样(否则未真正随机)")
	}
}

func TestBootstrapMeanCIEmpty(t *testing.T) {
	if _, ok := BootstrapMeanCI(nil, 0.95, 100, 1); ok {
		t.Fatal("空样本应返回 false")
	}
}

func TestReportScorerValuesSkipsSkipped(t *testing.T) {
	rep := Aggregate("d", []CaseResult{
		{CaseID: "a", Scores: []Score{{Scorer: "m", Value: 1, Passed: true}}},
		{CaseID: "b", Scores: []Score{{Scorer: "m", Value: 0, Passed: false}}},
		{CaseID: "c", Scores: []Score{{Scorer: "m", Value: 0, Skipped: true, Passed: true}}},
	})
	got := rep.ScorerValues("m")
	if len(got) != 2 {
		t.Fatalf("跳过的用例不应计入样本: %v", got)
	}
	if _, ok := rep.MeanCI("missing", 0.95, 100, 1); ok {
		t.Fatal("未知打分器应返回 false")
	}
}

// 离散指标 + 小样本时,自助区间边界常恰好落在 0;浮点残差不得把它误判为显著。
func TestIntervalExcludesZeroTolerance(t *testing.T) {
	cases := []struct {
		name string
		ci   Interval
		want bool
	}{
		{"下界恰为 0", Interval{Lo: 0, Hi: 0.2222}, false},
		{"下界为浮点残差", Interval{Lo: 5.55e-17, Hi: 0.2222}, false},
		{"上界恰为 0", Interval{Lo: -0.125, Hi: 0}, false},
		{"上界为负残差", Interval{Lo: -0.125, Hi: -3e-17}, false},
		{"确实全为正", Interval{Lo: 0.01, Hi: 0.3}, true},
		{"确实全为负", Interval{Lo: -0.3, Hi: -0.01}, true},
		{"跨 0", Interval{Lo: -0.1, Hi: 0.2}, false},
	}
	for _, c := range cases {
		if got := c.ci.ExcludesZero(); got != c.want {
			t.Errorf("%s: ExcludesZero(%+v) = %v, want %v", c.name, c.ci, got, c.want)
		}
	}
}
