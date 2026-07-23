package main

import "testing"

func TestExplorerTokenURLByChain(t *testing.T) {
	tests := []struct {
		chain string
		want  string
	}{
		{"base", "https://basescan.org/token/0xabc"},
		{"ethereum", "https://etherscan.io/token/0xabc"},
		{"eth", "https://etherscan.io/token/0xabc"},
		{"bsc", "https://bscscan.com/token/0xabc"},
		{"optimism", "https://optimistic.etherscan.io/token/0xabc"},
		{"op", "https://optimistic.etherscan.io/token/0xabc"},
		{"polygon", "https://polygonscan.com/token/0xabc"},
		{"matic", "https://polygonscan.com/token/0xabc"},
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

func TestChineseMarketURLByChain(t *testing.T) {
	tests := []struct {
		chain string
		want  string
	}{
		{"base", "https://www.geckoterminal.com/zh/base/pools/0x1111111111111111111111111111111111111111"},
		{"ethereum", "https://www.geckoterminal.com/zh/eth/pools/0x1111111111111111111111111111111111111111"},
		{"eth", "https://www.geckoterminal.com/zh/eth/pools/0x1111111111111111111111111111111111111111"},
		{"bsc", "https://www.geckoterminal.com/zh/bsc/pools/0x1111111111111111111111111111111111111111"},
		{"optimism", "https://www.geckoterminal.com/zh/optimism/pools/0x1111111111111111111111111111111111111111"},
		{"op", "https://www.geckoterminal.com/zh/optimism/pools/0x1111111111111111111111111111111111111111"},
		{"polygon", "https://www.geckoterminal.com/zh/polygon_pos/pools/0x1111111111111111111111111111111111111111"},
		{"matic", "https://www.geckoterminal.com/zh/polygon_pos/pools/0x1111111111111111111111111111111111111111"},
		{"arbitrum", "https://www.geckoterminal.com/zh/arbitrum/pools/0x1111111111111111111111111111111111111111"},
		{"arb", "https://www.geckoterminal.com/zh/arbitrum/pools/0x1111111111111111111111111111111111111111"},
	}
	for _, tt := range tests {
		if got := chineseMarketURL(Token{Chain: tt.chain, PoolAddress: "0x1111111111111111111111111111111111111111"}); got != tt.want {
			t.Fatalf("chain %s: got %q want %q", tt.chain, got, tt.want)
		}
	}
}

func TestChineseMarketURLRejectsMissingExactPool(t *testing.T) {
	if got := chineseMarketURL(Token{Chain: "bsc", Address: "0x1111111111111111111111111111111111111111"}); got != "" {
		t.Fatalf("missing exact pool should not create a potentially wrong market link: %q", got)
	}
}

func TestPageScrollClampsToVirtualPage(t *testing.T) {
	oldDPI, oldScale, oldScroll := app.dpi, app.uiScale, app.pageScroll
	defer func() {
		app.dpi, app.uiScale, app.pageScroll = oldDPI, oldScale, oldScroll
	}()
	app.dpi, app.uiScale, app.pageScroll = 96, 1, 0

	if !scrollPage(88) || app.pageScroll != 88 {
		t.Fatalf("first page scroll = %d, want 88", app.pageScroll)
	}
	scrollPage(1000)
	if app.pageScroll != 360 {
		t.Fatalf("bottom page scroll = %d, want 360", app.pageScroll)
	}
	scrollPage(-1000)
	if app.pageScroll != 0 {
		t.Fatalf("top page scroll = %d, want 0", app.pageScroll)
	}
}
