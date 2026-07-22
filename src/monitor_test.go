package main

import (
	"encoding/json"
	"testing"
	"time"
)

func monitorToken(price, liquidity float64) Token {
	now := time.Now()
	return Token{Chain: "base", Address: "0x1111111111111111111111111111111111111111", Symbol: "TEST", Name: "Test Token", Price: price, Liquidity: liquidity, Score: 80, Security: "已验证", Source: "unit-test", UpdatedAt: now}
}

func TestMonitorStateJSONRoundTrip(t *testing.T) {
	m := NewMonitorState()
	token := monitorToken(1, 100_000)
	m.Toggle(token, time.Now())
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var restored MonitorState
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	restored.Normalize()
	if restored.Count() != 1 || !restored.IsWatching(token) {
		t.Fatal("watch list did not survive JSON persistence")
	}
}

func TestMonitorToggleAndSnapshot(t *testing.T) {
	m := NewMonitorState()
	now := time.Now()
	token := monitorToken(1, 100_000)
	if !m.Toggle(token, now) || !m.IsWatching(token) || m.Count() != 1 {
		t.Fatal("token was not added to the watch list")
	}
	if len(m.Snapshots[monitorIdentity(token.Chain, token.Address)]) != 1 {
		t.Fatal("initial trusted snapshot was not recorded")
	}
	if m.Toggle(token, now.Add(time.Second)) || m.IsWatching(token) {
		t.Fatal("token was not removed from the watch list")
	}
}

func TestMonitorRaisesPriceAndLiquidityAlerts(t *testing.T) {
	m := NewMonitorState()
	now := time.Now()
	initial := monitorToken(1, 100_000)
	m.Toggle(initial, now)
	changed := monitorToken(0.75, 60_000)
	changed.UpdatedAt = now.Add(time.Minute)
	alerts := m.Observe([]Token{changed}, now.Add(time.Minute))
	kinds := map[string]bool{}
	for _, alert := range alerts {
		kinds[alert.Kind] = true
	}
	if !kinds["price-move"] || !kinds["liquidity-drop"] {
		t.Fatalf("expected price and liquidity alerts, got %+v", alerts)
	}
}

func TestMonitorAlertCooldown(t *testing.T) {
	m := NewMonitorState()
	now := time.Now()
	initial := monitorToken(1, 100_000)
	m.Toggle(initial, now)
	changed := monitorToken(0.5, 100_000)
	m.Observe([]Token{changed}, now.Add(time.Minute))
	firstCount := len(m.Alerts)
	m.Observe([]Token{changed}, now.Add(2*time.Minute))
	if len(m.Alerts) != firstCount {
		t.Fatalf("duplicate alerts were not cooled down: %d -> %d", firstCount, len(m.Alerts))
	}
}
