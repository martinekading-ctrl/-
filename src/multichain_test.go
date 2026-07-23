//go:build windows

package main

import "testing"

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
