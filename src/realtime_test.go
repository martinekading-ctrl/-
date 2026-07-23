//go:build windows

package main

import (
	"bufio"
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func testABIAddress(address string) string {
	return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(address), "0x")
}

func TestFreeRealtimeRegistryIsThreeChainDeepMode(t *testing.T) {
	modules := realtimeModules()
	if len(modules) != 3 {
		t.Fatalf("realtime module count=%d, want 3", len(modules))
	}
	want := map[string]bool{"base": true, "bsc": true, "arbitrum": true}
	for _, m := range modules {
		if !want[m.Key] || len(m.WSRPCs) == 0 {
			t.Fatalf("unexpected or incomplete free realtime module: %+v", m)
		}
		for _, endpoint := range realtimeWSEndpoints(m) {
			if !strings.HasPrefix(endpoint, "ws") {
				t.Fatalf("non-websocket realtime endpoint %q", endpoint)
			}
		}
	}
}

func TestRealtimeFactoryLogQueuesOnlyTheUnknownLeg(t *testing.T) {
	m, ok := chainByKey("bsc")
	if !ok {
		t.Fatal("BSC module missing")
	}
	var factory factorySpec
	for _, f := range m.Factories {
		if f.Kind == "v2" {
			factory = f
			break
		}
	}
	if factory.Address == "" {
		t.Fatal("BSC v2 factory missing")
	}
	unknown := "0x1111111111111111111111111111111111111111"
	pool := "0x2222222222222222222222222222222222222222"
	log := rpcLog{
		Address: factory.Address,
		Topics:  []string{factory.Topic, testABIAddress("0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c"), testABIAddress(unknown)},
		Data:    testABIAddress(pool), BlockNumber: "0x123", TransactionHash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LogIndex: "0x7",
	}
	got := candidatesFromFactoryLog(m, factory, log, time.Now(), true)
	if len(got) != 1 {
		t.Fatalf("candidate count=%d, want 1: %+v", len(got), got)
	}
	if got[0].TokenAddress != unknown || got[0].PoolAddress != pool || got[0].BlockNumber != 0x123 || !got[0].Realtime || got[0].EventLogIndex != "0x7" {
		t.Fatalf("unexpected event candidate: %+v", got[0])
	}
}

func TestRealtimeLogFilterIncludesEveryFactoryAndTopic(t *testing.T) {
	m, _ := chainByKey("base")
	filter := realtimeLogFilter(m)
	addresses, ok := filter["address"].([]string)
	if !ok || len(addresses) != len(m.Factories) {
		t.Fatalf("address filter=%#v", filter["address"])
	}
	topics, ok := filter["topics"].([]any)
	if !ok || len(topics) != 1 {
		t.Fatalf("topic filter=%#v", filter["topics"])
	}
	firstTopics, ok := topics[0].([]string)
	if !ok || len(firstTopics) != len(m.Factories) {
		t.Fatalf("first-topic alternatives=%#v", topics[0])
	}
}

func TestRealtimeSummaryMarksStaleConnectionsOffline(t *testing.T) {
	now := time.Now()
	h := &freeRealtimeHub{running: true, chains: map[string]realtimeChainHealth{}, pending: map[string]multiCandidate{}}
	for _, m := range realtimeModules() {
		h.chains[m.Key] = realtimeChainHealth{Chain: m.Key, Connected: true, LastMessageAt: now}
	}
	s := h.snapshot()
	if s.Connected != 3 || s.Expected != 3 {
		t.Fatalf("live summary=%+v", s)
	}
	h.chains["base"] = realtimeChainHealth{Chain: "base", Connected: true, LastMessageAt: now.Add(-realtimeStaleAfter - time.Second)}
	if stale := h.snapshot(); stale.Connected != 2 {
		t.Fatalf("stale endpoint counted as live: %+v", stale)
	}
}

func TestRealtimeWebSocketReadsStandardTextFrame(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ws := &realtimeWS{conn: client, reader: bufio.NewReader(client)}
	go func() { _, _ = server.Write([]byte{0x81, 0x02, '{', '}'}) }()
	payload, err := ws.readText()
	if err != nil || string(payload) != "{}" {
		t.Fatalf("read websocket text=%q err=%v", payload, err)
	}
}

// This is deliberately opt-in: CI and normal unit tests must stay deterministic
// and must not consume a public RPC quota. It is used before a desktop release
// to prove the current free endpoints accept both subscriptions.
func TestRealtimePublicEndpointsWhenRequested(t *testing.T) {
	if testing.Short() || strings.TrimSpace(os.Getenv("MCTR_LIVE_WSS_TEST")) != "1" {
		t.Skip("set MCTR_LIVE_WSS_TEST=1 to exercise public WebSocket endpoints")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, m := range realtimeModules() {
		endpoints := realtimeWSEndpoints(m)
		if len(endpoints) == 0 {
			t.Fatalf("%s has no realtime endpoint", m.Key)
		}
		ws, err := dialRealtimeWebSocket(ctx, endpoints[0])
		if err != nil {
			t.Fatalf("%s websocket dial: %v", m.Key, err)
		}
		_, _, err = ws.subscribe(ctx, realtimeLogFilter(m))
		_ = ws.Close()
		if err != nil {
			t.Fatalf("%s websocket subscribe: %v", m.Key, err)
		}
	}
}
