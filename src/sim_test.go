package main

import (
	"testing"
	"time"
)

func testQuote(price float64) SimQuote {
	return SimQuote{Address: "0x1111111111111111111111111111111111111111", Symbol: "TST", Name: "Test", Price: price, Liquidity: 100000, Score: 90, Security: "已验证", Buys: 200, Sells: 100, Time: time.Now()}
}

func TestBuyAndManualSellIncludeCosts(t *testing.T) {
	s := NewSimState()
	now := time.Now()
	q := testQuote(1)
	p, err := s.Buy(q, 5, "manual", now)
	if err != nil {
		t.Fatal(err)
	}
	if !(p.Quantity < 5 && s.Cash == 95) {
		t.Fatalf("unexpected buy qty=%v cash=%v", p.Quantity, s.Cash)
	}
	tr, err := s.Sell(p.ID, 1, q, "manual", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if tr.PnL >= 0 {
		t.Fatalf("round trip at unchanged price must lose costs, pnl=%v", tr.PnL)
	}
	if len(s.Positions) != 0 || len(s.Trades) != 1 {
		t.Fatal("position/trade accounting failed")
	}
}

func TestStopLossClosesPosition(t *testing.T) {
	s := NewSimState()
	now := time.Now()
	p, err := s.Buy(testQuote(1), 5, "manual", now)
	if err != nil {
		t.Fatal(err)
	}
	events := s.Update([]SimQuote{testQuote(0.85)}, now.Add(time.Minute))
	if len(events) == 0 || len(s.Positions) != 0 || len(s.Trades) != 1 {
		t.Fatalf("stop loss did not close: p=%d t=%d e=%v", len(s.Positions), len(s.Trades), events)
	}
	if s.Trades[0].PositionID != p.ID {
		t.Fatal("wrong position closed")
	}
}

func TestTakeProfitPartialThenFinal(t *testing.T) {
	s := NewSimState()
	s.Config.BaseSlippagePct = 0.01
	s.Config.DexFeePct = 0.01
	s.Config.GasUSDC = 0.0001
	now := time.Now()
	p, err := s.Buy(testQuote(1), 5, "manual", now)
	if err != nil {
		t.Fatal(err)
	}
	s.Update([]SimQuote{testQuote(1.16)}, now.Add(time.Minute))
	if len(s.Positions) != 1 || !s.Positions[0].TP1Done || !(s.Positions[0].Quantity < p.Quantity) {
		t.Fatalf("tp1 failed: %+v", s.Positions)
	}
	s.Update([]SimQuote{testQuote(1.30)}, now.Add(2*time.Minute))
	if len(s.Positions) != 0 || len(s.Trades) != 2 {
		t.Fatalf("tp2 failed: positions=%d trades=%d", len(s.Positions), len(s.Trades))
	}
}

func TestDailyLossLimitBlocksAutoEntry(t *testing.T) {
	s := NewSimState()
	s.AutoEnabled = true
	now := time.Now()
	s.Trades = append(s.Trades, SimTrade{PnL: -6, ClosedAt: now})
	events := s.AutoEvaluate([]SimQuote{testQuote(1)}, now)
	if len(events) == 0 || len(s.Positions) != 0 {
		t.Fatal("daily loss limit not enforced")
	}
}

func TestAutoStrategyRequiresHistoryAndCanEnter(t *testing.T) {
	s := NewSimState()
	s.AutoEnabled = true
	s.AutoProfile = AutoProfileConservative
	s.Config.BaseSlippagePct = 0.01
	now := time.Now()
	q := testQuote(1.06)
	q.TaxKnown = true
	// A three-minute rise, pullback, then three rising marks below the peak.
	prices := []float64{1.00, 1.01, 1.03, 1.06, 1.08, 1.05, 1.045, 1.048, 1.052, 1.056, 1.060}
	start := now.Add(-3*time.Minute - 10*time.Second)
	for i, p := range prices {
		tm := start.Add(time.Duration(i) * 19 * time.Second)
		s.Snapshots[q.Address] = append(s.Snapshots[q.Address], PriceSnapshot{Time: tm, Price: p, Liquidity: q.Liquidity})
	}
	q.Price = prices[len(prices)-1]
	events := s.AutoEvaluate([]SimQuote{q}, now)
	if len(events) == 0 || len(s.Positions) != 1 {
		t.Fatalf("expected auto entry, events=%v positions=%d", events, len(s.Positions))
	}
}

func TestAutoProfilesPermitTestingWithoutBypassingHardRisk(t *testing.T) {
	s := NewSimState()
	s.AutoEnabled = true
	s.AutoProfile = AutoProfileTest
	now := time.Now()
	q := testQuote(1.02)
	q.Chain = "bsc"
	q.Score = 58
	q.Liquidity = 20000
	q.Security = "安全未验证"
	q.TaxKnown = false
	start := now.Add(-75 * time.Second)
	for i, px := range []float64{1.00, 1.003, 1.006, 1.010, 1.014, 1.02} {
		s.Snapshots[simKey(q.Chain, q.Address)] = append(s.Snapshots[simKey(q.Chain, q.Address)], PriceSnapshot{Time: start.Add(time.Duration(i) * 15 * time.Second), Price: px, Liquidity: q.Liquidity})
	}
	if events := s.AutoEvaluate([]SimQuote{q}, now); len(events) == 0 || len(s.Positions) != 1 {
		t.Fatalf("test profile should open a paper position, events=%v positions=%d", events, len(s.Positions))
	}

	s2 := NewSimState()
	s2.AutoEnabled = true
	s2.AutoProfile = AutoProfileTest
	bad := q
	bad.Security = "严重风险"
	bad.Score = 0
	s2.Snapshots[simKey(bad.Chain, bad.Address)] = s.Snapshots[simKey(q.Chain, q.Address)]
	if events := s2.AutoEvaluate([]SimQuote{bad}, now); len(events) != 0 || len(s2.Positions) != 0 {
		t.Fatalf("hard risk must stay blocked, events=%v positions=%d", events, len(s2.Positions))
	}
}

func TestSameAddressOnDifferentChainsCanCoexist(t *testing.T) {
	s := NewSimState()
	now := time.Now()
	q1 := testQuote(1)
	q1.Chain = "base"
	q2 := testQuote(2)
	q2.Chain = "arbitrum"
	if _, err := s.Buy(q1, 5, "manual", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Buy(q2, 5, "manual", now); err != nil {
		t.Fatalf("same hex address on another chain should be independent: %v", err)
	}
	if len(s.Positions) != 2 {
		t.Fatalf("expected two cross-chain positions, got %d", len(s.Positions))
	}
}

func TestValidationGroupsPartialExitsByCompletePosition(t *testing.T) {
	s := NewSimState()
	s.Config.MinValidationTrades = 2
	s.MaxDrawdown = 5
	now := time.Now()
	s.Trades = []SimTrade{
		{PositionID: 10, PnL: 0.30, CostAllocated: 2.5, ClosedAt: now.Add(-3 * time.Minute), Automated: true},
		{PositionID: 10, PnL: 0.20, CostAllocated: 2.5, ClosedAt: now.Add(-2 * time.Minute), Automated: true},
		{PositionID: 11, PnL: -0.25, CostAllocated: 5, ClosedAt: now.Add(-time.Minute), Automated: true},
		{PositionID: 12, PnL: 99, CostAllocated: 5, ClosedAt: now, Automated: false},
	}
	v := s.Validation()
	if v.ClosedPositions != 2 || v.Wins != 1 || v.Losses != 1 {
		t.Fatalf("partial exits must be one complete outcome: %+v", v)
	}
	if !v.Passed || v.NetPnL <= 0 || v.ProfitFactor < s.Config.MinProfitFactor {
		t.Fatalf("expected validation pass after costs and drawdown gates: %+v", v)
	}
}

func TestValidationExcludesStillOpenAutomatedPosition(t *testing.T) {
	s := NewSimState()
	s.Positions = []SimPosition{{ID: 10, Automated: true, Quantity: 1}}
	s.Trades = []SimTrade{{PositionID: 10, PnL: 0.3, ClosedAt: time.Now(), Automated: true}}
	if v := s.Validation(); v.ClosedPositions != 0 {
		t.Fatalf("open partial position must not count as completed: %+v", v)
	}
}

func TestConsecutiveLossesTriggerPaperCircuitBreaker(t *testing.T) {
	s := NewSimState()
	s.Config.DailyLossLimit = 10
	now := time.Now()
	for i := int64(1); i <= 3; i++ {
		s.Trades = append(s.Trades, SimTrade{PositionID: i, PnL: -0.2, ClosedAt: now.Add(time.Duration(i-3) * time.Minute), Automated: true})
	}
	events := s.AutoEvaluate([]SimQuote{testQuote(1)}, now)
	if len(events) == 0 || len(s.Positions) != 0 {
		t.Fatalf("loss streak should pause new entries: events=%v", events)
	}
}

func TestV28MigrationEnablesOnlyPaperAutomation(t *testing.T) {
	s := &SimState{Version: 1, Config: DefaultSimConfig(), Cash: 100}
	s.Config.MaxHoldingHours = 24
	s.Config.LiquidityDropPct = 30
	s.Normalize()
	if s.Version != 3 || !s.AutoEnabled || s.AutoProfile != AutoProfileExplore {
		t.Fatalf("expected V2.11 exploration migration: version=%d auto=%v profile=%d", s.Version, s.AutoEnabled, s.AutoProfile)
	}
	if s.Config.MaxHoldingHours != 6 || s.Config.LiquidityDropPct != 25 {
		t.Fatalf("expected V2.8 risk defaults: %+v", s.Config)
	}
}

func TestExploreProfileCreatesCappedPaperSampleAndClosesIt(t *testing.T) {
	s := NewSimState()
	s.AutoEnabled = true
	s.AutoProfile = AutoProfileExplore
	now := time.Now()
	q := testQuote(1.03)
	q.Chain = "base"
	q.Score = 15
	q.Liquidity = 6000
	q.Security = "安全未验证"
	q.TaxKnown = false
	q.Buys, q.Sells = 1, 0
	q.Time = now
	key := simKey(q.Chain, q.Address)
	for i, px := range []float64{1.00, 1.01, 1.02, 1.03} {
		s.Snapshots[key] = append(s.Snapshots[key], PriceSnapshot{Time: now.Add(time.Duration(-30+i*10) * time.Second), Price: px, Liquidity: q.Liquidity})
	}
	if events := s.AutoEvaluate([]SimQuote{q}, now); len(events) == 0 || len(s.Positions) != 1 {
		t.Fatalf("explore profile should create a paper sample: events=%v positions=%d", events, len(s.Positions))
	}
	p := s.Positions[0]
	if !p.Exploratory || p.EntryCost > 1 {
		t.Fatalf("explore position must be flagged and capped: %+v", p)
	}
	q.Time = now.Add(31 * time.Minute)
	if events := s.Update([]SimQuote{q}, q.Time); len(events) == 0 || len(s.Positions) != 0 || len(s.Trades) != 1 || !s.Trades[0].Exploratory {
		t.Fatalf("explore sample should close on its short horizon: events=%v positions=%d trades=%+v", events, len(s.Positions), s.Trades)
	}
	if v := s.Validation(); v.ClosedPositions != 0 {
		t.Fatalf("explore samples must not be counted as strict validation: %+v", v)
	}
}

func TestNearMissCreatesAndClosesShadowSample(t *testing.T) {
	s := NewSimState()
	s.AutoEnabled = true
	s.AutoProfile = AutoProfileExplore
	now := time.Now()
	q := testQuote(1)
	q.Score = 14
	q.Liquidity = 7000
	q.Security = "安全未验证"
	q.TaxKnown = false
	q.Time = now
	s.AutoEvaluate([]SimQuote{q}, now)
	if len(s.ShadowSamples) != 1 || s.LastEntryStats.Rejections["评分不足"] != 1 || s.LastEntryStats.ShadowStarted != 1 {
		t.Fatalf("near miss should be diagnosed and shadowed: stats=%+v shadows=%+v", s.LastEntryStats, s.ShadowSamples)
	}
	q.Price = 1.05
	q.Time = now.Add(31 * time.Minute)
	if events := s.UpdateShadows([]SimQuote{q}, q.Time); len(events) == 0 || len(s.ShadowSamples) != 0 || len(s.ShadowOutcomes) != 1 {
		t.Fatalf("shadow sample should close into research telemetry: events=%v active=%d outcomes=%d", events, len(s.ShadowSamples), len(s.ShadowOutcomes))
	}
}

func TestAutoEntryRejectsUnstableLiquidity(t *testing.T) {
	s := NewSimState()
	s.AutoProfile = AutoProfileTest
	now := time.Now()
	q := testQuote(1.02)
	q.Chain = "bsc"
	q.Score = 60
	q.Liquidity = 15000
	q.Security = "安全未验证"
	q.Time = now
	start := now.Add(-75 * time.Second)
	for i, px := range []float64{1.00, 1.004, 1.008, 1.012, 1.016, 1.02} {
		liq := 20000.0
		if i >= 3 {
			liq = 15000
		}
		s.Snapshots[simKey(q.Chain, q.Address)] = append(s.Snapshots[simKey(q.Chain, q.Address)], PriceSnapshot{Time: start.Add(time.Duration(i) * 15 * time.Second), Price: px, Liquidity: liq})
	}
	if ok, reason := s.autoEligible(q, now); ok || reason != "观察期流动性不稳定" {
		t.Fatalf("expected unstable liquidity rejection, ok=%v reason=%q", ok, reason)
	}
}

func TestSecurityDeteriorationForcesExit(t *testing.T) {
	s := NewSimState()
	now := time.Now()
	p, err := s.Buy(testQuote(1), 5, "自动策略：test", now)
	if err != nil {
		t.Fatal(err)
	}
	bad := testQuote(1.05)
	bad.Security = "严重风险"
	bad.Score = 0
	s.Update([]SimQuote{bad}, now.Add(time.Minute))
	if len(s.Positions) != 0 || len(s.Trades) != 1 || s.Trades[0].PositionID != p.ID {
		t.Fatalf("security deterioration must exit: positions=%d trades=%+v", len(s.Positions), s.Trades)
	}
}
