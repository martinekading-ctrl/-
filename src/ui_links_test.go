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

func TestChineseTokenURLByChain(t *testing.T) {
	tests := []struct {
		chain string
		want  string
	}{
		{"base", "https://www.oklink.com/zh-hans/base/token/0xabc"},
		{"ethereum", "https://www.oklink.com/zh-hans/eth/token/0xabc"},
		{"eth", "https://www.oklink.com/zh-hans/eth/token/0xabc"},
		{"bsc", "https://www.oklink.com/zh-hans/bsc/token/0xabc"},
		{"optimism", "https://www.oklink.com/zh-hans/optimism/token/0xabc"},
		{"op", "https://www.oklink.com/zh-hans/optimism/token/0xabc"},
		{"polygon", "https://www.oklink.com/zh-hans/polygon/token/0xabc"},
		{"matic", "https://www.oklink.com/zh-hans/polygon/token/0xabc"},
		{"arbitrum", "https://www.oklink.com/zh-hans/arbitrum-one/token/0xabc"},
		{"arb", "https://www.oklink.com/zh-hans/arbitrum-one/token/0xabc"},
	}
	for _, tt := range tests {
		if got := chineseTokenURL(tt.chain, "0xabc"); got != tt.want {
			t.Fatalf("chain %s: got %q want %q", tt.chain, got, tt.want)
		}
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
