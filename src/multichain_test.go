//go:build windows

package main

import (
	"testing"
	"time"
)

func TestExpandedEVMChainRegistry(t *testing.T) {
	want := map[string]struct {
		chainID string
		dexSlug string
	}{
		"ethereum": {"1", "ethereum"},
		"base":     {"8453", "base"},
		"bsc":      {"56", "bsc"},
		"optimism": {"10", "optimism"},
		"polygon":  {"137", "polygon"},
		"arbitrum": {"42161", "arbitrum"},
	}
	if len(chainModules) != len(want) {
		t.Fatalf("chain module count = %d, want %d", len(chainModules), len(want))
	}
	for key, expected := range want {
		m, ok := chainByKey(key)
		if !ok {
			t.Fatalf("missing %s module", key)
		}
		if m.ChainID != expected.chainID || m.DexSlug != expected.dexSlug {
			t.Fatalf("%s registry = chainID %s dex %s", key, m.ChainID, m.DexSlug)
		}
		if len(m.RPCs) < 2 || len(m.Factories) == 0 || len(m.KnownAssets) == 0 {
			t.Fatalf("%s lacks redundant RPCs, factories, or known assets", key)
		}
		if m.Confirmations == 0 {
			t.Fatalf("%s has no confirmation margin", key)
		}
		for _, f := range m.Factories {
			if !validAddress(f.Address) {
				t.Fatalf("%s factory %s has invalid address %q", key, f.Name, f.Address)
			}
		}
	}
}

func TestConfirmedBlock(t *testing.T) {
	if got := confirmedBlock(100, 6); got != 94 {
		t.Fatalf("confirmed block = %d, want 94", got)
	}
	if got := confirmedBlock(6, 6); got != 0 {
		t.Fatalf("under-confirmed block = %d, want 0", got)
	}
}

func TestPaperGasIsChainAware(t *testing.T) {
	s := NewSimState()
	s.Config.GasUSDC = 0.01
	if got := s.paperGasUSDC("ethereum"); got < 1.25 {
		t.Fatalf("ethereum gas = %.2f, want at least 1.25", got)
	}
	if got := s.paperGasUSDC("optimism"); got < 0.04 {
		t.Fatalf("optimism gas = %.2f, want at least 0.04", got)
	}
	if got := s.paperGasUSDC("polygon"); got < 0.03 {
		t.Fatalf("polygon gas = %.2f, want at least 0.03", got)
	}
	if got := s.paperGasUSDC("base"); got != 0.01 {
		t.Fatalf("base gas = %.2f, want configured 0.01", got)
	}
}

func strictTestPair(token, pool string, created int64) dexPair {
	p := dexPair{PairAddress: pool, PriceUSD: "1", PairCreatedAt: created}
	p.BaseToken.Address = token
	p.Liquidity.USD = 15_000
	p.Txns.H1.Buys, p.Txns.H1.Sells = 6, 2
	p.Txns.H24.Buys, p.Txns.H24.Sells = 20, 8
	return p
}

func strictTestSecurity() map[string]any {
	return map[string]any{
		"is_open_source": "1", "is_honeypot": "0", "cannot_sell_all": "0", "is_blacklisted": "0",
		"owner_change_balance": "0", "selfdestruct": "0", "is_proxy": "0", "is_mintable": "0",
		"hidden_owner": "0", "transfer_pausable": "0", "slippage_modifiable": "0", "personal_slippage_modifiable": "0",
		"trading_cooldown": "0", "anti_whale_modifiable": "0", "external_call": "0",
		"owner_percent": "0.01", "creator_percent": "0.01", "holder_count": "120",
		"holders": []any{
			map[string]any{"percent": "0.08", "is_locked": "0", "tag": ""},
			map[string]any{"percent": "0.12", "is_locked": "0", "tag": ""},
		},
		"lp_holders": []any{
			map[string]any{"percent": "0.85", "is_locked": "1", "tag": "Locker"},
			map[string]any{"percent": "0.15", "is_locked": "0", "tag": ""},
		},
	}
}

func TestExactPoolNeverFallsBackToLargestHistoricalPool(t *testing.T) {
	token := "0x1111111111111111111111111111111111111111"
	exactPool := "0x2222222222222222222222222222222222222222"
	oldPool := "0x3333333333333333333333333333333333333333"
	now := time.Now()
	exact := strictTestPair(token, exactPool, now.Add(-5*time.Minute).UnixMilli())
	old := strictTestPair(token, oldPool, now.Add(-72*time.Hour).UnixMilli())
	old.Liquidity.USD = 8_000_000

	q := assessExactPool(multiCandidate{TokenAddress: token, PoolAddress: exactPool}, []dexPair{old, exact})
	if !q.Exact || q.Pair.PairAddress != exactPool {
		t.Fatalf("exact pool not selected: %+v", q)
	}
	if !q.HasOlderIndexedPool {
		t.Fatal("older indexed market must disqualify an old token's new auxiliary pool")
	}
}

func TestStrictMarketFirstQualification(t *testing.T) {
	token := "0x1111111111111111111111111111111111111111"
	pool := "0x2222222222222222222222222222222222222222"
	pair := strictTestPair(token, pool, time.Now().Add(-5*time.Minute).UnixMilli())
	q := assessExactPool(multiCandidate{TokenAddress: token, PoolAddress: pool}, []dexPair{pair})
	tok := Token{Price: 1, Liquidity: pair.Liquidity.USD, Security: "已验证", TaxKnown: true, BuyTaxPct: 1, SellTaxPct: 1, LPLockKnown: true, LPLockPct: 0.85}
	if ok, reason := strictMarketFirstQualification(q, tok, strictTestSecurity(), true); !ok {
		t.Fatalf("safe first-market candidate rejected: %s", reason)
	}

	pair.Txns.H1.Buys = 1
	q = assessExactPool(multiCandidate{TokenAddress: token, PoolAddress: pool}, []dexPair{pair})
	if ok, _ := strictMarketFirstQualification(q, tok, strictTestSecurity(), true); ok {
		t.Fatal("candidate with insufficient early buying passed")
	}
}

func TestStrictMarketFirstQualificationRejectsUnsafeConcentrationAndLiquidityOwnership(t *testing.T) {
	token := "0x1111111111111111111111111111111111111111"
	pool := "0x2222222222222222222222222222222222222222"
	pair := strictTestPair(token, pool, time.Now().Add(-5*time.Minute).UnixMilli())
	q := assessExactPool(multiCandidate{TokenAddress: token, PoolAddress: pool}, []dexPair{pair})
	tok := Token{Price: 1, Liquidity: pair.Liquidity.USD, Security: "已验证", TaxKnown: true, BuyTaxPct: 1, SellTaxPct: 1, LPLockKnown: true, LPLockPct: 0.85}

	concentrated := strictTestSecurity()
	concentrated["holders"] = []any{map[string]any{"percent": "0.16", "is_locked": "0", "tag": ""}}
	if ok, _ := strictMarketFirstQualification(q, tok, concentrated, true); ok {
		t.Fatal("a 16% unlocked top holder must never pass the automatic strict funnel")
	}

	unlockedLP := strictTestSecurity()
	tok.LPLockPct = 0.79
	if ok, _ := strictMarketFirstQualification(q, tok, unlockedLP, true); ok {
		t.Fatal("liquidity below the strict 80% lock threshold must not pass")
	}

	missingField := strictTestSecurity()
	delete(missingField, "slippage_modifiable")
	tok.LPLockPct = 0.85
	if ok, _ := strictMarketFirstQualification(q, tok, missingField, true); ok {
		t.Fatal("missing security data must fail closed for automatic strict entries")
	}
}

func TestResolveTrackedCandidatePreservesEntryPool(t *testing.T) {
	entryPool := "0x2222222222222222222222222222222222222222"
	newerPool := "0x3333333333333333333333333333333333333333"
	resolved := resolveTrackedCandidate(
		multiCandidate{Chain: "base", TokenAddress: "0x1111111111111111111111111111111111111111", PoolAddress: entryPool, Factory: "模拟持仓跟踪"},
		Token{PoolAddress: "0x4444444444444444444444444444444444444444", BlockNumber: 99}, true,
		multiCandidate{Chain: "base", TokenAddress: "0x1111111111111111111111111111111111111111", PoolAddress: newerPool, Factory: "Other", BlockNumber: 100}, true,
	)
	if resolved.PoolAddress != entryPool {
		t.Fatalf("entry pool was overwritten: got %s want %s", resolved.PoolAddress, entryPool)
	}

	fromCache := resolveTrackedCandidate(
		multiCandidate{Chain: "base", TokenAddress: "0x1111111111111111111111111111111111111111"},
		Token{PoolAddress: entryPool, BlockNumber: 99}, true,
		multiCandidate{Chain: "base", TokenAddress: "0x1111111111111111111111111111111111111111", PoolAddress: newerPool}, true,
	)
	if fromCache.PoolAddress != entryPool {
		t.Fatalf("cached entry pool was overwritten: got %s want %s", fromCache.PoolAddress, entryPool)
	}
}

func TestTopMultiDisplayShowsStrictThenRecentReadOnlyReview(t *testing.T) {
	now := time.Now()
	cache := map[string]Token{
		"base|strict": {Chain: "base", Address: "strict", Score: 20, PotentialEligible: true, UpdatedAt: now},
		"base|review": {Chain: "base", Address: "review", Score: 99, PotentialStage: "淘汰：流动性不足", UpdatedAt: now},
		"base|stale":  {Chain: "base", Address: "stale", Score: 99, PotentialStage: "淘汰：旧缓存", UpdatedAt: now.Add(-7 * time.Hour)},
	}
	rows := topMultiDisplay(cache, 100)
	if len(rows) != 2 || rows[0].Address != "strict" || rows[1].Address != "review" {
		t.Fatalf("strict signal must precede a recent read-only review row: %+v", rows)
	}
	if rows[1].PotentialEligible {
		t.Fatal("review row must stay ineligible for every trade path")
	}
}

func TestRiskSnapshotAlertsOnlyOnMaterialDeterioration(t *testing.T) {
	old := Token{Chain: "base", Symbol: "TST", Liquidity: 100_000, LPLockKnown: true, LPLockPct: 0.90, CreatorPercent: 0.02, TopHolderPercent: 0.10, RiskCheckedAt: time.Now()}
	next := old
	next.Liquidity = 60_000
	next.LPLockPct = 0.70
	next.CreatorPercent = 0.09
	next.TopHolderPercent = 0.25
	alerts := riskSnapshotAlerts(old, next)
	if len(alerts) != 4 {
		t.Fatalf("expected four material risk alerts, got %v", alerts)
	}
	if len(strictRiskWarnings(Token{Chain: "base", Symbol: "TST"})) == 0 {
		t.Fatal("an unverified LP lock must be made visible on a new strict candidate")
	}
}
