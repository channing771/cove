package eval

import (
	"context"
	"errors"
)

// Evaluator 用一组打分器在数据集上评估一个 Runner。
type Evaluator struct {
	Runner  Runner
	Scorers []Scorer
}

// Run 串行跑完数据集,逐用例调 Runner 与全部 Scorer,聚合成 Report。
func (e *Evaluator) Run(ctx context.Context, ds *Dataset) (*Report, error) {
	if ds == nil {
		return nil, errors.New("eval: nil dataset")
	}
	if e.Runner == nil {
		return nil, errors.New("eval: nil runner")
	}
	cases := make([]CaseResult, 0, len(ds.Cases))
	for _, c := range ds.Cases {
		record, err := e.Runner.Run(ctx, c)
		if err != nil {
			return nil, err
		}
		cr := CaseResult{
			CaseID:      c.ID,
			Tags:        c.Tags,
			LatencyMS:   record.Latency.Milliseconds(),
			Iterations:  record.Iterations(),
			TotalTokens: record.Usage.TotalTokens,
			CostUSD:     record.Usage.CostUSD,
			StopReason:  record.StopReason(),
			Answer:      record.Answer(),
		}
		for _, s := range e.Scorers {
			cr.Scores = append(cr.Scores, s.Score(ctx, c, record))
		}
		cases = append(cases, cr)
	}
	return Aggregate(ds.Name, cases), nil
}
