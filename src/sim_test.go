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
