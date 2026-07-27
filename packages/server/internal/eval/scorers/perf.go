package scorers

import (
	"context"
	"fmt"

	"github.com/boxify/api-go/internal/eval"
)

// LatencyBudget 断言运行墙钟延迟不超过 expect.latency_ms 毫秒。
func LatencyBudget() eval.Scorer {
	return fn{"latency_budget", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		budgetMS, ok := eval.ExpectFloat(c, "latency_ms")
		if !ok {
			return skipped("latency_budget")
		}
		gotMS := float64(r.Latency.Milliseconds())
		if gotMS <= budgetMS {
			return passed("latency_budget", fmt.Sprintf("%.0fms <= %.0fms", gotMS, budgetMS))
		}
		return failed("latency_budget", fmt.Sprintf("%.0fms > %.0fms", gotMS, budgetMS))
	}}
}

// IterationBudget 断言迭代次数不超过 expect.iteration_budget。
func IterationBudget() eval.Scorer {
	return fn{"iteration_budget", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		budget, ok := eval.ExpectInt(c, "iteration_budget")
		if !ok {
			return skipped("iteration_budget")
		}
		if r.Iterations() <= budget {
			return passed("iteration_budget", "")
		}
		return failed("iteration_budget", fmt.Sprintf("iters %d > %d", r.Iterations(), budget))
	}}
}

// TokenBudget 断言累计 token 不超过 expect.token_budget。
//
// 累计 token 为 0(如流式路径无 usage)时判跳过,避免误判通过。
func TokenBudget() eval.Scorer {
	return fn{"token_budget", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		budget, ok := eval.ExpectInt(c, "token_budget")
		if !ok {
			return skipped("token_budget")
		}
		if r.Usage.TotalTokens == 0 {
			return skipped("token_budget")
		}
		if int(r.Usage.TotalTokens) <= budget {
			return passed("token_budget", fmt.Sprintf("%d <= %d", r.Usage.TotalTokens, budget))
		}
		return failed("token_budget", fmt.Sprintf("%d > %d", r.Usage.TotalTokens, budget))
	}}
}

// CostBudget 断言折算成本不超过 expect.cost_usd。成本为 0 时判跳过。
func CostBudget() eval.Scorer {
	return fn{"cost_budget", func(_ context.Context, c eval.Case, r eval.RunRecord) eval.Score {
		budget, ok := eval.ExpectFloat(c, "cost_usd")
		if !ok {
			return skipped("cost_budget")
		}
		if r.Usage.CostUSD == 0 {
			return skipped("cost_budget")
		}
		if r.Usage.CostUSD <= budget {
			return passed("cost_budget", fmt.Sprintf("$%.4f <= $%.4f", r.Usage.CostUSD, budget))
		}
		return failed("cost_budget", fmt.Sprintf("$%.4f > $%.4f", r.Usage.CostUSD, budget))
	}}
}
