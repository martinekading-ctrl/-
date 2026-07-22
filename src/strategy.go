package main

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// SimValidation evaluates complete automated positions, not individual partial
// exits. That prevents a half take-profit from being counted as a separate win.
type SimValidation struct {
	Status            string
	Detail            string
	Passed            bool
	ClosedPositions   int
	RequiredPositions int
	Wins              int
	Losses            int
	WinRate           float64
	NetPnL            float64
	GrossProfit       float64
	GrossLoss         float64
	ProfitFactor      float64
	Expectancy        float64
	MaxDrawdown       float64
	ConsecutiveLosses int
	LastClosedAt      time.Time
}

type simPositionOutcome struct {
	PositionID int64
	PnL        float64
	Cost       float64
	ClosedAt   time.Time
}

func (s *SimState) automatedOutcomes() []simPositionOutcome {
	open := make(map[int64]bool, len(s.Positions))
	for _, p := range s.Positions {
		open[p.ID] = true
	}
	byID := map[int64]simPositionOutcome{}
	for _, tr := range s.Trades {
		if !tr.Automated || open[tr.PositionID] {
			continue
		}
		o := byID[tr.PositionID]
		o.PositionID = tr.PositionID
		o.PnL += tr.PnL
		o.Cost += tr.CostAllocated
		if tr.ClosedAt.After(o.ClosedAt) {
			o.ClosedAt = tr.ClosedAt
		}
		byID[tr.PositionID] = o
	}
	out := make([]simPositionOutcome, 0, len(byID))
	for _, o := range byID {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClosedAt.Before(out[j].ClosedAt) })
	return out
}

func (s *SimState) Validation() SimValidation {
	s.Normalize()
	v := SimValidation{RequiredPositions: s.Config.MinValidationTrades}
	equity := s.Config.InitialCash
	peak := equity
	for _, o := range s.automatedOutcomes() {
		v.ClosedPositions++
		v.NetPnL += o.PnL
		v.LastClosedAt = o.ClosedAt
		if o.PnL > 0 {
			v.Wins++
			v.GrossProfit += o.PnL
			v.ConsecutiveLosses = 0
		} else {
			v.Losses++
			v.GrossLoss += -o.PnL
			v.ConsecutiveLosses++
		}
		equity += o.PnL
		if equity > peak {
			peak = equity
		}
		if peak > 0 {
			dd := (peak - equity) / peak * 100
			if dd > v.MaxDrawdown {
				v.MaxDrawdown = dd
			}
		}
	}
	if v.ClosedPositions > 0 {
		v.WinRate = float64(v.Wins) / float64(v.ClosedPositions) * 100
		v.Expectancy = v.NetPnL / float64(v.ClosedPositions)
	}
	if v.GrossLoss > 0 {
		v.ProfitFactor = v.GrossProfit / v.GrossLoss
	} else if v.GrossProfit > 0 {
		v.ProfitFactor = math.Inf(1)
	}
	if v.ClosedPositions < v.RequiredPositions {
		v.Status = "样本收集中"
		v.Detail = "完整自动交易不足，不能判断策略是否具备正期望"
		return v
	}
	v.Passed = v.NetPnL > 0 && v.ProfitFactor >= s.Config.MinProfitFactor && v.MaxDrawdown <= s.Config.MaxValidationDrawdown
	if v.Passed {
		v.Status = "纸面验证通过"
		v.Detail = "达到样本、净收益、利润因子和最大回撤门槛"
	} else {
		v.Status = "纸面验证未通过"
		v.Detail = "继续观察或调整策略，禁止据此启用真钱交易"
	}
	return v
}

func (v SimValidation) ProfitFactorText() string {
	if math.IsInf(v.ProfitFactor, 1) {
		return "∞"
	}
	return fmt.Sprintf("%.2f", v.ProfitFactor)
}

func (s *SimState) riskPause(now time.Time) (bool, time.Duration) {
	v := s.Validation()
	if v.ConsecutiveLosses < s.Config.MaxConsecutiveLosses || v.LastClosedAt.IsZero() {
		return false, 0
	}
	until := v.LastClosedAt.Add(time.Duration(s.Config.LossCooldownMinutes * float64(time.Minute)))
	if !now.Before(until) {
		return false, 0
	}
	return true, until.Sub(now)
}

func (s *SimState) dynamicPositionSize(q SimQuote) float64 {
	amount := math.Min(s.Config.PositionSize, s.Cash)
	// At most two basis points of observed pool liquidity. This keeps the paper
	// fill from pretending a thin pool can absorb the same size as a deep pool.
	if liquidityCap := q.Liquidity * 0.0002; liquidityCap > 0 {
		amount = math.Min(amount, liquidityCap)
	}
	streak := s.Validation().ConsecutiveLosses
	if streak == 1 {
		amount *= 0.75
	} else if streak >= 2 {
		amount *= 0.50
	}
	if amount < 1 {
		return 0
	}
	return math.Floor(amount*100) / 100
}

func (s *SimState) chainPositionCount(chain string) int {
	count := 0
	for _, p := range s.Positions {
		if normalizeChain(p.Chain) == normalizeChain(chain) {
			count++
		}
	}
	return count
}
