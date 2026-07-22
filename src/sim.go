package main

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// SimConfig contains deliberately conservative defaults for a small paper account.
// All percentages use human units (for example 8 means 8%).
type SimConfig struct {
	InitialCash           float64 `json:"initial_cash"`
	PositionSize          float64 `json:"position_size"`
	MaxPositions          int     `json:"max_positions"`
	DailyLossLimit        float64 `json:"daily_loss_limit"`
	StopLossPct           float64 `json:"stop_loss_pct"`
	TakeProfit1Pct        float64 `json:"take_profit1_pct"`
	TakeProfit2Pct        float64 `json:"take_profit2_pct"`
	TrailingStopPct       float64 `json:"trailing_stop_pct"`
	LiquidityDropPct      float64 `json:"liquidity_drop_pct"`
	MaxHoldingHours       float64 `json:"max_holding_hours"`
	DexFeePct             float64 `json:"dex_fee_pct"`
	GasUSDC               float64 `json:"gas_usdc"`
	BaseSlippagePct       float64 `json:"base_slippage_pct"`
	MaxSlippagePct        float64 `json:"max_slippage_pct"`
	MinScore              int     `json:"min_score"`
	MinLiquidity          float64 `json:"min_liquidity"`
	MaxTaxPct             float64 `json:"max_tax_pct"`
	ObserveMinutes        float64 `json:"observe_minutes"`
	AutoCooldownMinutes   float64 `json:"auto_cooldown_minutes"`
	MaxConsecutiveLosses  int     `json:"max_consecutive_losses"`
	LossCooldownMinutes   float64 `json:"loss_cooldown_minutes"`
	MinValidationTrades   int     `json:"min_validation_trades"`
	MinProfitFactor       float64 `json:"min_profit_factor"`
	MaxValidationDrawdown float64 `json:"max_validation_drawdown"`
}

func DefaultSimConfig() SimConfig {
	return SimConfig{
		InitialCash: 100, PositionSize: 5, MaxPositions: 3, DailyLossLimit: 5,
		StopLossPct: 8, TakeProfit1Pct: 12, TakeProfit2Pct: 20,
		TrailingStopPct: 8, LiquidityDropPct: 25, MaxHoldingHours: 6,
		DexFeePct: 0.30, GasUSDC: 0.01, BaseSlippagePct: 0.80, MaxSlippagePct: 12,
		MinScore: 80, MinLiquidity: 50_000, MaxTaxPct: 5,
		ObserveMinutes: 3, AutoCooldownMinutes: 30,
		MaxConsecutiveLosses: 3, LossCooldownMinutes: 180,
		MinValidationTrades: 30, MinProfitFactor: 1.20, MaxValidationDrawdown: 12,
	}
}

type SimQuote struct {
	Chain      string    `json:"chain"`
	Address    string    `json:"address"`
	Symbol     string    `json:"symbol"`
	Name       string    `json:"name"`
	Price      float64   `json:"price"`
	Liquidity  float64   `json:"liquidity"`
	BuyTaxPct  float64   `json:"buy_tax_pct"`
	SellTaxPct float64   `json:"sell_tax_pct"`
	TaxKnown   bool      `json:"tax_known"`
	Score      int       `json:"score"`
	Security   string    `json:"security"`
	Buys       int       `json:"buys"`
	Sells      int       `json:"sells"`
	Volume24   float64   `json:"volume24"`
	AgeHours   float64   `json:"age_hours"`
	Source     string    `json:"source"`
	Time       time.Time `json:"time"`
}

type PriceSnapshot struct {
	Time      time.Time `json:"time"`
	Price     float64   `json:"price"`
	Liquidity float64   `json:"liquidity"`
}

type SimPosition struct {
	ID               int64     `json:"id"`
	Chain            string    `json:"chain"`
	Address          string    `json:"address"`
	Symbol           string    `json:"symbol"`
	Name             string    `json:"name"`
	Quantity         float64   `json:"quantity"`
	InitialQuantity  float64   `json:"initial_quantity"`
	EntryPrice       float64   `json:"entry_price"`
	EntryCost        float64   `json:"entry_cost"`
	RemainingCost    float64   `json:"remaining_cost"`
	EntryLiquidity   float64   `json:"entry_liquidity"`
	CurrentPrice     float64   `json:"current_price"`
	CurrentLiquidity float64   `json:"current_liquidity"`
	HighestPrice     float64   `json:"highest_price"`
	LowestPrice      float64   `json:"lowest_price"`
	BuyTaxPct        float64   `json:"buy_tax_pct"`
	SellTaxPct       float64   `json:"sell_tax_pct"`
	EntryScore       int       `json:"entry_score"`
	EntrySecurity    string    `json:"entry_security"`
	LastQuoteAt      time.Time `json:"last_quote_at"`
	OpenedAt         time.Time `json:"opened_at"`
	TP1Done          bool      `json:"tp1_done"`
	Automated        bool      `json:"automated"`
	EntryReason      string    `json:"entry_reason"`
}

type SimTrade struct {
	ID            int64     `json:"id"`
	Chain         string    `json:"chain"`
	PositionID    int64     `json:"position_id"`
	Address       string    `json:"address"`
	Symbol        string    `json:"symbol"`
	Quantity      float64   `json:"quantity"`
	EntryPrice    float64   `json:"entry_price"`
	ExitPrice     float64   `json:"exit_price"`
	CostAllocated float64   `json:"cost_allocated"`
	NetProceeds   float64   `json:"net_proceeds"`
	Fees          float64   `json:"fees"`
	PnL           float64   `json:"pnl"`
	PnLPct        float64   `json:"pnl_pct"`
	OpenedAt      time.Time `json:"opened_at"`
	ClosedAt      time.Time `json:"closed_at"`
	Reason        string    `json:"reason"`
	Automated     bool      `json:"automated"`
}

type SimMetrics struct {
	Cash          float64
	PositionValue float64
	Equity        float64
	NetPnL        float64
	RealizedPnL   float64
	UnrealizedPnL float64
	WinRate       float64
	MaxDrawdown   float64
	Trades        int
	Wins          int
}

type SimState struct {
	Version       int                        `json:"version"`
	Config        SimConfig                  `json:"config"`
	Cash          float64                    `json:"cash"`
	Positions     []SimPosition              `json:"positions"`
	Trades        []SimTrade                 `json:"trades"`
	Snapshots     map[string][]PriceSnapshot `json:"snapshots"`
	AutoEnabled   bool                       `json:"auto_enabled"`
	AutoProfile   int                        `json:"auto_profile"`
	NextID        int64                      `json:"next_id"`
	EquityPeak    float64                    `json:"equity_peak"`
	MaxDrawdown   float64                    `json:"max_drawdown"`
	LastAutoEntry map[string]time.Time       `json:"last_auto_entry"`
}

func NewSimState() *SimState {
	cfg := DefaultSimConfig()
	return &SimState{
		Version: 2, Config: cfg, Cash: cfg.InitialCash, Snapshots: map[string][]PriceSnapshot{},
		AutoEnabled: true, NextID: 1, EquityPeak: cfg.InitialCash,
		LastAutoEntry: map[string]time.Time{}, AutoProfile: AutoProfileStandard,
	}
}

func (s *SimState) Normalize() {
	d := DefaultSimConfig()
	oldVersion := s.Version
	if s.Version == 0 {
		s.Version = 1
	}
	if s.Config.InitialCash <= 0 {
		s.Config = d
	}
	if s.Config.PositionSize <= 0 {
		s.Config.PositionSize = d.PositionSize
	}
	if s.Config.MaxPositions <= 0 {
		s.Config.MaxPositions = d.MaxPositions
	}
	if s.Config.DailyLossLimit <= 0 {
		s.Config.DailyLossLimit = d.DailyLossLimit
	}
	if s.Config.StopLossPct <= 0 {
		s.Config.StopLossPct = d.StopLossPct
	}
	if s.Config.TakeProfit1Pct <= 0 {
		s.Config.TakeProfit1Pct = d.TakeProfit1Pct
	}
	if s.Config.TakeProfit2Pct <= 0 {
		s.Config.TakeProfit2Pct = d.TakeProfit2Pct
	}
	if s.Config.TrailingStopPct <= 0 {
		s.Config.TrailingStopPct = d.TrailingStopPct
	}
	if s.Config.LiquidityDropPct <= 0 {
		s.Config.LiquidityDropPct = d.LiquidityDropPct
	}
	if s.Config.MaxHoldingHours <= 0 {
		s.Config.MaxHoldingHours = d.MaxHoldingHours
	}
	if s.Config.DexFeePct <= 0 {
		s.Config.DexFeePct = d.DexFeePct
	}
	if s.Config.GasUSDC <= 0 {
		s.Config.GasUSDC = d.GasUSDC
	}
	if s.Config.BaseSlippagePct <= 0 {
		s.Config.BaseSlippagePct = d.BaseSlippagePct
	}
	if s.Config.MaxSlippagePct <= 0 {
		s.Config.MaxSlippagePct = d.MaxSlippagePct
	}
	if s.Config.MinScore <= 0 {
		s.Config.MinScore = d.MinScore
	}
	if s.Config.MinLiquidity <= 0 {
		s.Config.MinLiquidity = d.MinLiquidity
	}
	if s.Config.MaxTaxPct <= 0 {
		s.Config.MaxTaxPct = d.MaxTaxPct
	}
	if s.Config.ObserveMinutes <= 0 {
		s.Config.ObserveMinutes = d.ObserveMinutes
	}
	if s.Config.AutoCooldownMinutes <= 0 {
		s.Config.AutoCooldownMinutes = d.AutoCooldownMinutes
	}
	if s.Config.MaxConsecutiveLosses <= 0 {
		s.Config.MaxConsecutiveLosses = d.MaxConsecutiveLosses
	}
	if s.Config.LossCooldownMinutes <= 0 {
		s.Config.LossCooldownMinutes = d.LossCooldownMinutes
	}
	if s.Config.MinValidationTrades <= 0 {
		s.Config.MinValidationTrades = d.MinValidationTrades
	}
	if s.Config.MinProfitFactor <= 0 {
		s.Config.MinProfitFactor = d.MinProfitFactor
	}
	if s.Config.MaxValidationDrawdown <= 0 {
		s.Config.MaxValidationDrawdown = d.MaxValidationDrawdown
	}
	if s.Snapshots == nil {
		s.Snapshots = map[string][]PriceSnapshot{}
	}
	if s.LastAutoEntry == nil {
		s.LastAutoEntry = map[string]time.Time{}
	}
	if s.AutoProfile < AutoProfileConservative || s.AutoProfile > AutoProfileTest {
		s.AutoProfile = AutoProfileStandard
	}
	for i := range s.Positions {
		s.Positions[i].Chain = normalizeChain(s.Positions[i].Chain)
		s.Positions[i].Address = normalizeAddress(s.Positions[i].Address)
	}
	for i := range s.Trades {
		s.Trades[i].Chain = normalizeChain(s.Trades[i].Chain)
		s.Trades[i].Address = normalizeAddress(s.Trades[i].Address)
	}
	if s.NextID <= 0 {
		s.NextID = 1
	}
	if s.Cash < 0 {
		s.Cash = 0
	}
	if s.EquityPeak <= 0 {
		s.EquityPeak = s.Config.InitialCash
	}
	if oldVersion < 2 {
		// V2.8 turns on paper automation for existing local simulation accounts.
		// This migration never enables wallet access or real transaction submission.
		s.Version = 2
		s.AutoEnabled = true
		if s.Config.MaxHoldingHours == 24 {
			s.Config.MaxHoldingHours = d.MaxHoldingHours
		}
		if s.Config.LiquidityDropPct == 30 {
			s.Config.LiquidityDropPct = d.LiquidityDropPct
		}
	}
}

func normalizeAddress(a string) string { return strings.ToLower(strings.TrimSpace(a)) }
func normalizeChain(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	if c == "" {
		return "base"
	}
	if c == "arb" {
		return "arbitrum"
	}
	return c
}
func simKey(chain, address string) string {
	return normalizeChain(chain) + "|" + normalizeAddress(address)
}

const (
	AutoProfileConservative = iota
	AutoProfileStandard
	AutoProfileTest
)

func (s *SimState) ProfileName() string {
	s.Normalize()
	switch s.AutoProfile {
	case AutoProfileConservative:
		return "保守"
	case AutoProfileTest:
		return "测试"
	default:
		return "标准"
	}
}

func (s *SimState) CycleProfile() string {
	s.Normalize()
	s.AutoProfile = (s.AutoProfile + 1) % 3
	return s.ProfileName()
}

type autoRules struct {
	MinScore        int
	MinLiquidity    float64
	MaxTaxPct       float64
	ObserveMinutes  float64
	MinTrend        float64
	MaxTrend        float64
	MinSnapshots    int
	RequireVerified bool
	RequireTaxKnown bool
	RequirePullback bool
	MinBuySellRatio float64
	MaxAgeHours     float64
	MaxStepMovePct  float64
}

func (s *SimState) rules() autoRules {
	s.Normalize()
	switch s.AutoProfile {
	case AutoProfileConservative:
		return autoRules{80, 50000, 5, 3, 1, 12, 8, true, true, true, 1.25, 6, 8}
	case AutoProfileTest:
		return autoRules{55, 10000, 12, 1, 0.2, 20, 5, false, false, false, 1.0, 24, 20}
	default:
		return autoRules{70, 25000, 8, 2, 0.5, 15, 6, true, true, true, 1.15, 12, 12}
	}
}
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (s *SimState) slippagePct(notional, liquidity float64) float64 {
	impact := 0.0
	if liquidity > 0 {
		impact = notional / liquidity * 100
	}
	return clamp(s.Config.BaseSlippagePct+impact, s.Config.BaseSlippagePct, s.Config.MaxSlippagePct)
}

func (s *SimState) AddSnapshots(quotes []SimQuote, now time.Time) {
	s.Normalize()
	cutoff := now.Add(-30 * time.Minute)
	for _, q := range quotes {
		if q.Price <= 0 || q.Address == "" {
			continue
		}
		a := simKey(q.Chain, q.Address)
		h := s.Snapshots[a]
		if len(h) > 0 && now.Sub(h[len(h)-1].Time) < 2*time.Second {
			continue
		}
		h = append(h, PriceSnapshot{Time: now, Price: q.Price, Liquidity: q.Liquidity})
		start := 0
		for start < len(h) && h[start].Time.Before(cutoff) {
			start++
		}
		if start > 0 {
			h = append([]PriceSnapshot(nil), h[start:]...)
		}
		if len(h) > 400 {
			h = append([]PriceSnapshot(nil), h[len(h)-400:]...)
		}
		s.Snapshots[a] = h
	}
}

func (s *SimState) hasPositionOn(chain, address string) bool {
	k := simKey(chain, address)
	for _, p := range s.Positions {
		if simKey(p.Chain, p.Address) == k && p.Quantity > 0 {
			return true
		}
	}
	return false
}
func (s *SimState) hasPosition(address string) bool { return s.hasPositionOn("base", address) }

func (s *SimState) Buy(q SimQuote, amount float64, reason string, now time.Time) (SimPosition, error) {
	s.Normalize()
	q.Chain = normalizeChain(q.Chain)
	q.Address = normalizeAddress(q.Address)
	if q.Address == "" || q.Price <= 0 {
		return SimPosition{}, errors.New("当前没有可用价格")
	}
	if strings.Contains(q.Source, "演示") {
		return SimPosition{}, errors.New("演示数据不能写入正式模拟账户")
	}
	if s.hasPositionOn(q.Chain, q.Address) {
		return SimPosition{}, errors.New("该代币已经有模拟持仓")
	}
	if len(s.Positions) >= s.Config.MaxPositions {
		return SimPosition{}, fmt.Errorf("最多同时持有 %d 个代币", s.Config.MaxPositions)
	}
	if amount <= 0 {
		amount = s.Config.PositionSize
	}
	if amount > s.Cash {
		return SimPosition{}, errors.New("模拟余额不足")
	}
	if q.Liquidity <= 0 {
		return SimPosition{}, errors.New("缺少流动性数据，无法估算滑点")
	}
	if q.BuyTaxPct > 20 {
		return SimPosition{}, errors.New("买入税过高，模拟系统拒绝开仓")
	}

	slip := s.slippagePct(amount, q.Liquidity)
	fee := amount * s.Config.DexFeePct / 100
	spendable := amount - fee - s.Config.GasUSDC
	if spendable <= 0 {
		return SimPosition{}, errors.New("仓位金额不足以覆盖模拟成本")
	}
	executionPrice := q.Price * (1 + (slip+math.Max(q.BuyTaxPct, 0))/100)
	qty := spendable / executionPrice
	if qty <= 0 || math.IsNaN(qty) || math.IsInf(qty, 0) {
		return SimPosition{}, errors.New("模拟成交数量无效")
	}

	p := SimPosition{
		ID: s.NextID, Chain: q.Chain, Address: q.Address, Symbol: q.Symbol, Name: q.Name,
		Quantity: qty, InitialQuantity: qty, EntryPrice: executionPrice,
		EntryCost: amount, RemainingCost: amount, EntryLiquidity: q.Liquidity,
		CurrentPrice: q.Price, CurrentLiquidity: q.Liquidity,
		HighestPrice: q.Price, LowestPrice: q.Price, BuyTaxPct: q.BuyTaxPct, SellTaxPct: q.SellTaxPct,
		EntryScore: q.Score, EntrySecurity: q.Security, LastQuoteAt: q.Time,
		OpenedAt: now, Automated: strings.HasPrefix(reason, "自动策略："), EntryReason: reason,
	}
	if p.LastQuoteAt.IsZero() {
		p.LastQuoteAt = now
	}
	s.NextID++
	s.Cash -= amount
	s.Positions = append(s.Positions, p)
	return p, nil
}

func (s *SimState) liquidationValue(p SimPosition, price, liquidity float64) (net, fees float64) {
	if p.Quantity <= 0 || price <= 0 {
		return 0, 0
	}
	notional := p.Quantity * price
	slip := s.slippagePct(notional, liquidity)
	tax := math.Max(p.SellTaxPct, 0)
	afterImpact := notional * math.Max(0, 1-(slip+tax)/100)
	dexFee := afterImpact * s.Config.DexFeePct / 100
	fees = notional - afterImpact + dexFee + s.Config.GasUSDC
	net = afterImpact - dexFee - s.Config.GasUSDC
	if net < 0 {
		net = 0
	}
	return net, fees
}

func (s *SimState) Sell(positionID int64, fraction float64, q SimQuote, reason string, now time.Time) (SimTrade, error) {
	s.Normalize()
	idx := -1
	for i := range s.Positions {
		if s.Positions[i].ID == positionID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return SimTrade{}, errors.New("没有找到模拟持仓")
	}
	p := &s.Positions[idx]
	if q.Price <= 0 {
		q.Price = p.CurrentPrice
	}
	if q.Liquidity <= 0 {
		q.Liquidity = p.CurrentLiquidity
	}
	if q.Price <= 0 {
		return SimTrade{}, errors.New("当前没有可用卖出价格")
	}
	if fraction <= 0 || fraction > 1 {
		fraction = 1
	}
	qty := p.Quantity * fraction
	if fraction > 0.999999 {
		qty = p.Quantity
	}
	costAllocated := p.RemainingCost * (qty / p.Quantity)

	temp := *p
	temp.Quantity = qty
	net, fees := s.liquidationValue(temp, q.Price, q.Liquidity)
	pnl := net - costAllocated
	pnlPct := 0.0
	if costAllocated > 0 {
		pnlPct = pnl / costAllocated * 100
	}
	tr := SimTrade{
		ID: s.NextID, Chain: p.Chain, PositionID: p.ID, Address: p.Address, Symbol: p.Symbol,
		Quantity: qty, EntryPrice: p.EntryPrice, ExitPrice: q.Price,
		CostAllocated: costAllocated, NetProceeds: net, Fees: fees,
		PnL: pnl, PnLPct: pnlPct, OpenedAt: p.OpenedAt, ClosedAt: now, Reason: reason,
		Automated: p.Automated,
	}
	s.NextID++
	s.Cash += net
	p.Quantity -= qty
	p.RemainingCost -= costAllocated
	if p.Quantity <= p.InitialQuantity*1e-9 || fraction > 0.999999 {
		s.Positions = append(s.Positions[:idx], s.Positions[idx+1:]...)
	}
	s.Trades = append(s.Trades, tr)
	if len(s.Trades) > 1000 {
		s.Trades = append([]SimTrade(nil), s.Trades[len(s.Trades)-1000:]...)
	}
	return tr, nil
}

func quoteMap(quotes []SimQuote) map[string]SimQuote {
	m := make(map[string]SimQuote, len(quotes))
	for _, q := range quotes {
		q.Chain = normalizeChain(q.Chain)
		q.Address = normalizeAddress(q.Address)
		m[simKey(q.Chain, q.Address)] = q
	}
	return m
}

// Update applies marks and deterministic exit rules. Returned strings are human-readable strategy events.
func (s *SimState) Update(quotes []SimQuote, now time.Time) []string {
	s.Normalize()
	qm := quoteMap(quotes)
	events := []string{}
	ids := make([]int64, 0, len(s.Positions))
	for i := range s.Positions {
		p := &s.Positions[i]
		q, ok := qm[simKey(p.Chain, p.Address)]
		if !ok || q.Price <= 0 {
			continue
		}
		p.CurrentPrice, p.CurrentLiquidity = q.Price, q.Liquidity
		p.SellTaxPct = q.SellTaxPct
		p.LastQuoteAt = q.Time
		if p.LastQuoteAt.IsZero() {
			p.LastQuoteAt = now
		}
		if p.HighestPrice <= 0 || q.Price > p.HighestPrice {
			p.HighestPrice = q.Price
		}
		if p.LowestPrice <= 0 || q.Price < p.LowestPrice {
			p.LowestPrice = q.Price
		}
		ids = append(ids, p.ID)
	}
	// Work from stable IDs because partial/full sells modify the slice.
	for _, id := range ids {
		idx := -1
		for i := range s.Positions {
			if s.Positions[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		p := s.Positions[idx]
		q, ok := qm[simKey(p.Chain, p.Address)]
		if !ok || q.Price <= 0 {
			continue
		}
		net, _ := s.liquidationValue(p, q.Price, q.Liquidity)
		ret := 0.0
		if p.RemainingCost > 0 {
			ret = (net - p.RemainingCost) / p.RemainingCost * 100
		}
		reason := ""
		if q.Security == "严重风险" || q.Score <= 0 {
			reason = "安全状态恶化，紧急退出"
		} else if p.EntryScore > 0 && q.Score > 0 && p.EntryScore-q.Score >= 20 {
			reason = "质量分较入场下降 20 分"
		} else if p.EntryLiquidity > 0 && q.Liquidity > 0 && q.Liquidity <= p.EntryLiquidity*(1-s.Config.LiquidityDropPct/100) {
			reason = "流动性下降达到紧急退出线"
		} else if ret <= -s.Config.StopLossPct {
			reason = fmt.Sprintf("止损 %.1f%%", s.Config.StopLossPct)
		} else if now.Sub(p.OpenedAt).Hours() >= s.Config.MaxHoldingHours {
			reason = "持仓达到最长时间"
		} else if p.TP1Done && ret <= 0.5 {
			reason = "第一止盈后回落至成本保护线"
		} else if p.HighestPrice >= p.EntryPrice*1.08 && q.Price <= p.HighestPrice*(1-s.Config.TrailingStopPct/100) {
			reason = fmt.Sprintf("从最高价回撤 %.1f%%", s.Config.TrailingStopPct)
		}
		if reason != "" {
			if tr, err := s.Sell(id, 1, q, reason, now); err == nil {
				events = append(events, fmt.Sprintf("%s %s，净盈亏 %+.2f USDC", tr.Symbol, reason, tr.PnL))
			}
			continue
		}
		// TP1 is a partial exit; TP2 closes the remaining position.
		idx = -1
		for i := range s.Positions {
			if s.Positions[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		p = s.Positions[idx]
		net, _ = s.liquidationValue(p, q.Price, q.Liquidity)
		if p.RemainingCost > 0 {
			ret = (net - p.RemainingCost) / p.RemainingCost * 100
		}
		if ret >= s.Config.TakeProfit2Pct {
			if tr, err := s.Sell(id, 1, q, fmt.Sprintf("止盈 %.1f%%", s.Config.TakeProfit2Pct), now); err == nil {
				events = append(events, fmt.Sprintf("%s 达到第二止盈，净盈亏 %+.2f USDC", tr.Symbol, tr.PnL))
			}
		} else if ret >= s.Config.TakeProfit1Pct && !p.TP1Done {
			// Mark first so a failed UI refresh cannot repeat the same partial exit.
			for i := range s.Positions {
				if s.Positions[i].ID == id {
					s.Positions[i].TP1Done = true
				}
			}
			if tr, err := s.Sell(id, 0.5, q, fmt.Sprintf("第一止盈 %.1f%%，卖出一半", s.Config.TakeProfit1Pct), now); err == nil {
				events = append(events, fmt.Sprintf("%s 第一止盈，净盈亏 %+.2f USDC", tr.Symbol, tr.PnL))
			}
		}
	}
	s.updateDrawdown(quotes)
	return events
}

func (s *SimState) dailyRealized(now time.Time) float64 {
	y, m, d := now.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	total := 0.0
	for _, t := range s.Trades {
		if !t.ClosedAt.Before(start) {
			total += t.PnL
		}
	}
	return total
}

func (s *SimState) autoEligible(q SimQuote, now time.Time) (bool, string) {
	r := s.rules()
	if !q.Time.IsZero() && now.Sub(q.Time) > 90*time.Second {
		return false, "行情数据已过期"
	}
	if q.Price <= 0 || q.Liquidity < r.MinLiquidity {
		return false, "流动性或价格不足"
	}
	if q.Security == "严重风险" || q.Score <= 0 {
		return false, "严重风险或已淘汰"
	}
	if q.Score < r.MinScore {
		return false, "评分不足"
	}
	if q.AgeHours > r.MaxAgeHours {
		return false, "已超过自动狙击观察窗口"
	}
	if r.RequireVerified && q.Security != "已验证" {
		return false, "需要安全已验证"
	}
	if r.RequireTaxKnown && !q.TaxKnown {
		return false, "买卖税数据未确认"
	}
	if q.TaxKnown && (q.BuyTaxPct > r.MaxTaxPct || q.SellTaxPct > r.MaxTaxPct) {
		return false, "税费过高"
	}
	if q.Sells <= 0 || float64(q.Buys)/float64(q.Sells) < r.MinBuySellRatio {
		return false, "买卖强度不足"
	}
	if s.hasPositionOn(q.Chain, q.Address) {
		return false, "已有持仓"
	}
	if s.chainPositionCount(q.Chain) >= 1 {
		return false, "同一链已有自动风险敞口"
	}
	k := simKey(q.Chain, q.Address)
	if t := s.LastAutoEntry[k]; !t.IsZero() && now.Sub(t) < time.Duration(s.Config.AutoCooldownMinutes*float64(time.Minute)) {
		return false, "冷却中"
	}
	h := s.Snapshots[k]
	if len(h) == 0 {
		// One-time compatibility with V2.4 snapshots that were keyed by address only.
		h = s.Snapshots[normalizeAddress(q.Address)]
	}
	cutoff := now.Add(-time.Duration(r.ObserveMinutes * float64(time.Minute)))
	recent := make([]PriceSnapshot, 0, len(h))
	for _, x := range h {
		if !x.Time.Before(cutoff) {
			recent = append(recent, x)
		}
	}
	if len(recent) < r.MinSnapshots || recent[len(recent)-1].Time.Sub(recent[0].Time) < time.Duration(math.Max(0.5, r.ObserveMinutes-0.25)*float64(time.Minute)) {
		return false, "观察时间不足"
	}
	start, cur := recent[0].Price, recent[len(recent)-1].Price
	if start <= 0 {
		return false, "价格历史不足"
	}
	ret := (cur/start - 1) * 100
	if ret < r.MinTrend || ret > r.MaxTrend {
		return false, fmt.Sprintf("趋势不在 %.1f%%–%.1f%%", r.MinTrend, r.MaxTrend)
	}
	startLiquidity := recent[0].Liquidity
	if startLiquidity > 0 {
		minLiquidity := startLiquidity
		for _, x := range recent {
			if x.Liquidity > 0 && x.Liquidity < minLiquidity {
				minLiquidity = x.Liquidity
			}
		}
		if recent[len(recent)-1].Liquidity < startLiquidity*0.90 || minLiquidity < startLiquidity*0.80 {
			return false, "观察期流动性不稳定"
		}
	}
	for i := 1; i < len(recent); i++ {
		if recent[i-1].Price <= 0 {
			continue
		}
		step := math.Abs(recent[i].Price/recent[i-1].Price-1) * 100
		if step > r.MaxStepMovePct {
			return false, "短时价格跳变过大"
		}
	}
	high := start
	lowAfterHigh := math.MaxFloat64
	hi := 0
	for i, x := range recent {
		if x.Price > high {
			high, hi = x.Price, i
		}
	}
	for i := hi; i < len(recent); i++ {
		if recent[i].Price < lowAfterHigh {
			lowAfterHigh = recent[i].Price
		}
	}
	if r.RequirePullback {
		if lowAfterHigh == math.MaxFloat64 || high <= 0 || (high-lowAfterHigh)/high*100 < 1.0 {
			return false, "尚未形成可识别回调"
		}
		n := len(recent)
		if n < 3 || !(recent[n-1].Price > recent[n-2].Price && recent[n-2].Price >= recent[n-3].Price) {
			return false, "回调后尚未重新走强"
		}
		if cur >= high*0.997 {
			return false, "接近短线最高价，避免追高"
		}
		return true, fmt.Sprintf("%s档：放量回调后重新走强", s.ProfileName())
	}
	if len(recent) >= 2 && recent[len(recent)-1].Price < recent[len(recent)-2].Price {
		return false, "测试档仍要求最新价格不下跌"
	}
	return true, fmt.Sprintf("%s档：满足基础趋势条件", s.ProfileName())
}

func (s *SimState) AutoEvaluate(quotes []SimQuote, now time.Time) []string {
	s.Normalize()
	if !s.AutoEnabled {
		return nil
	}
	if len(s.Positions) >= s.Config.MaxPositions {
		return nil
	}
	if s.dailyRealized(now) <= -s.Config.DailyLossLimit {
		return []string{"今日模拟亏损达到上限，自动策略暂停开仓"}
	}
	if paused, remain := s.riskPause(now); paused {
		return []string{fmt.Sprintf("连续亏损熔断，剩余 %.0f 分钟", math.Ceil(remain.Minutes()))}
	}
	sorted := append([]SimQuote(nil), quotes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Score > sorted[j].Score })
	events := []string{}
	for _, q := range sorted {
		if len(s.Positions) >= s.Config.MaxPositions || s.Cash < 1 {
			break
		}
		ok, reason := s.autoEligible(q, now)
		if !ok {
			continue
		}
		amount := s.dynamicPositionSize(q)
		if amount <= 0 {
			continue
		}
		p, err := s.Buy(q, amount, "自动策略："+reason, now)
		if err != nil {
			continue
		}
		s.LastAutoEntry[simKey(q.Chain, q.Address)] = now
		events = append(events, fmt.Sprintf("自动模拟买入 %s，仓位 %.2f USDC", p.Symbol, p.EntryCost))
	}
	return events
}

func (s *SimState) quoteForPosition(p SimPosition, quotes []SimQuote) SimQuote {
	for _, q := range quotes {
		if simKey(q.Chain, q.Address) == simKey(p.Chain, p.Address) {
			return q
		}
	}
	return SimQuote{Chain: p.Chain, Address: p.Address, Symbol: p.Symbol, Name: p.Name, Price: p.CurrentPrice, Liquidity: p.CurrentLiquidity, SellTaxPct: p.SellTaxPct, Time: time.Now()}
}

func (s *SimState) Metrics(quotes []SimQuote) SimMetrics {
	s.Normalize()
	qm := quoteMap(quotes)
	m := SimMetrics{Cash: s.Cash, Equity: s.Cash}
	for _, p := range s.Positions {
		q, ok := qm[simKey(p.Chain, p.Address)]
		price, liq := p.CurrentPrice, p.CurrentLiquidity
		if ok && q.Price > 0 {
			price, liq = q.Price, q.Liquidity
		}
		v, _ := s.liquidationValue(p, price, liq)
		m.PositionValue += v
		m.UnrealizedPnL += v - p.RemainingCost
	}
	m.Equity += m.PositionValue
	for _, t := range s.Trades {
		m.RealizedPnL += t.PnL
		if t.PnL > 0 {
			m.Wins++
		}
	}
	m.NetPnL = m.Equity - s.Config.InitialCash
	m.Trades = len(s.Trades)
	if m.Trades > 0 {
		m.WinRate = float64(m.Wins) / float64(m.Trades) * 100
	}
	m.MaxDrawdown = s.MaxDrawdown
	return m
}

func (s *SimState) updateDrawdown(quotes []SimQuote) {
	m := s.Metrics(quotes)
	if m.Equity > s.EquityPeak {
		s.EquityPeak = m.Equity
	}
	if s.EquityPeak > 0 {
		dd := (s.EquityPeak - m.Equity) / s.EquityPeak * 100
		if dd > s.MaxDrawdown {
			s.MaxDrawdown = dd
		}
	}
}

func (s *SimState) Reset() {
	cfg := s.Config
	profile := s.AutoProfile
	*s = *NewSimState()
	s.Config = cfg
	s.AutoProfile = profile
	s.Cash = cfg.InitialCash
	s.EquityPeak = cfg.InitialCash
}
