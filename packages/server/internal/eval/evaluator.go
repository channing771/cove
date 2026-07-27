package eval

import "context"

// Evaluator 用一组打分器在数据集上评估一个 Runner。
type Evaluator struct {
	Runner  Runner
	Scorers []Scorer
}

// Run 串行跑完数据集,逐用例调 Runner 与全部 Scorer,聚合成 Report。
func (e *Evaluator) Run(ctx context.Context, ds *Dataset) (*Report, error) {
	rep := &Report{Dataset: ds.Name, Scorers: map[string]ScorerAgg{}}
	valueSum := map[string]float64{}
	valueCount := map[string]int{}

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
			Passed:      true,
		}
		for _, s := range e.Scorers {
			score := s.Score(ctx, c, record)
			if score.Err != nil && score.ErrText == "" {
				score.ErrText = score.Err.Error()
			}
			cr.Scores = append(cr.Scores, score)

			agg := rep.Scorers[score.Scorer]
			agg.Scorer = score.Scorer
			agg.Runs++
			switch {
			case score.Err != nil:
				agg.Errored++
				cr.Passed = false
			case score.Skipped:
				agg.Skipped++
			case score.Passed:
				agg.Passed++
				valueSum[score.Scorer] += score.Value
				valueCount[score.Scorer]++
			default:
				agg.Failed++
				cr.Passed = false
				valueSum[score.Scorer] += score.Value
				valueCount[score.Scorer]++
			}
			rep.Scorers[score.Scorer] = agg
		}
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
	return rep, nil
}
