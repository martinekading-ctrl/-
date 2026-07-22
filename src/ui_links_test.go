package main

import "testing"

func TestExplorerTokenURLByChain(t *testing.T) {
	tests := []struct {
		chain string
		want  string
	}{
		{"base", "https://basescan.org/token/0xabc"},
		{"bsc", "https://bscscan.com/token/0xabc"},
		{"arbitrum", "https://arbiscan.io/token/0xabc"},
		{"arb", "https://arbiscan.io/token/0xabc"},
	}
	for _, tt := range tests {
		if got := explorerTokenURL(tt.chain, "0xabc"); got != tt.want {
			t.Fatalf("chain %s: got %q want %q", tt.chain, got, tt.want)
		}
	}
}

func TestSelectedDEXURLPrefersSpecificMarketLink(t *testing.T) {
	tok := Token{Chain: "base", Address: "0xabc", DEXURL: "https://dexscreener.com/base/0xpool"}
	if got := selectedDEXURL(tok); got != tok.DEXURL {
		t.Fatalf("specific pair URL was not preserved: %q", got)
	}

	tok.DEXURL = ""
	if got := selectedDEXURL(tok); got != "https://dexscreener.com/base/0xabc" {
		t.Fatalf("unexpected token fallback URL: %q", got)
	}
}
