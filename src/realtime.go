//go:build windows

package main

// This file intentionally uses only the Go standard library. The desktop
// application must remain usable on a free plan without a vendor SDK or a
// paid websocket dependency. HTTP log reconciliation in multichain.go remains
// authoritative; this is an early-event queue and health layer, not a trading
// transport.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	realtimeStateVersion = 1
	realtimeStaleAfter   = 75 * time.Second
	realtimeMaxPayload   = 4 * 1024 * 1024
)

type realtimeChainHealth struct {
	Chain           string        `json:"chain"`
	Endpoint        string        `json:"endpoint,omitempty"`
	Connected       bool          `json:"connected"`
	LastConnectedAt time.Time     `json:"last_connected_at,omitempty"`
	LastMessageAt   time.Time     `json:"last_message_at,omitempty"`
	LastHead        uint64        `json:"last_head,omitempty"`
	LastHeadAt      time.Time     `json:"last_head_at,omitempty"`
	LastHeadLag     time.Duration `json:"-"`
	LastEventAt     time.Time     `json:"last_event_at,omitempty"`
	LastEventBlock  uint64        `json:"last_event_block,omitempty"`
	EventCount      int           `json:"event_count"`
	Reconnects      int           `json:"reconnects"`
	Failovers       int           `json:"failovers"`
	LastError       string        `json:"last_error,omitempty"`
}

type realtimeCheckpointFile struct {
	Version   int                            `json:"version"`
	UpdatedAt time.Time                      `json:"updated_at"`
	Chains    map[string]realtimeChainHealth `json:"chains"`
	Pending   []multiCandidate               `json:"pending"`
}

// realtimeSummary is intentionally small enough to cross the scanner/UI
// boundary without exposing a mutable map from a background goroutine.
type realtimeSummary struct {
	Expected  int
	Connected int
	Pending   int
	Running   bool
	Chains    []realtimeChainHealth
}

func (s realtimeSummary) Text() string {
	if s.Expected == 0 {
		return "事件监听未配置"
	}
	if s.Connected == 0 {
		return fmt.Sprintf("事件 0/%d（HTTP 回补仍可用）", s.Expected)
	}
	lag := s.HeadLagText()
	if lag == "" {
		return fmt.Sprintf("事件 %d/%d · 待分析 %d", s.Connected, s.Expected, s.Pending)
	}
	return fmt.Sprintf("事件 %d/%d · 待分析 %d · %s", s.Connected, s.Expected, s.Pending, lag)
}

func (s realtimeSummary) ShortText() string {
	if s.Expected == 0 {
		return "未配置"
	}
	return fmt.Sprintf("%d/%d", s.Connected, s.Expected)
}

// HeadLagText is an observable arrival-age indicator, not a latency promise.
// It compares the newest subscribed header's on-chain timestamp with the
// local receipt time and reports the slowest currently connected deep chain.
func (s realtimeSummary) HeadLagText() string {
	var worst time.Duration
	found := false
	for _, health := range s.Chains {
		if !health.Connected || health.LastHeadAt.IsZero() {
			continue
		}
		lag := health.LastHeadLag
		if lag < 0 {
			lag = 0
		}
		if !found || lag > worst {
			worst, found = lag, true
		}
	}
	if !found {
		return ""
	}
	return fmt.Sprintf("区块滞后 %.0fs", worst.Seconds())
}

type freeRealtimeHub struct {
	mu        sync.Mutex
	cancel    context.CancelFunc
	running   bool
	chains    map[string]realtimeChainHealth
	pending   map[string]multiCandidate
	lastSaved time.Time
}

var freeRealtime = newFreeRealtimeHub()

func realtimeStatePath() string { return filepath.Join(dataDir(), "free_realtime_v221.json") }

func newFreeRealtimeHub() *freeRealtimeHub {
	h := &freeRealtimeHub{chains: map[string]realtimeChainHealth{}, pending: map[string]multiCandidate{}}
	var stored realtimeCheckpointFile
	if b, err := os.ReadFile(realtimeStatePath()); err == nil && json.Unmarshal(b, &stored) == nil {
		for chain, health := range stored.Chains {
			health.Chain = normalizeChain(chain)
			health.Connected = false // a process restart always requires a new session.
			h.chains[health.Chain] = health
		}
		for _, c := range stored.Pending {
			if validAddress(c.TokenAddress) && validAddress(c.PoolAddress) {
				c.Chain = normalizeChain(c.Chain)
				h.pending[realtimeCandidateKey(c)] = c
			}
		}
	}
	for _, m := range realtimeModules() {
		if _, ok := h.chains[m.Key]; !ok {
			h.chains[m.Key] = realtimeChainHealth{Chain: m.Key}
		}
	}
	return h
}

func realtimeModules() []chainModule {
	modules := make([]chainModule, 0, 3)
	for _, m := range chainModules {
		if m.Realtime && len(m.WSRPCs) > 0 {
			modules = append(modules, m)
		}
	}
	return modules
}

func realtimeCandidateKey(c multiCandidate) string {
	return strings.Join([]string{normalizeChain(c.Chain), strings.ToLower(c.TokenAddress), strings.ToLower(c.PoolAddress), fmt.Sprintf("%d", c.BlockNumber)}, "|")
}

func (h *freeRealtimeHub) persistLocked(force bool) {
	now := time.Now()
	if !force && !h.lastSaved.IsZero() && now.Sub(h.lastSaved) < 4*time.Second {
		return
	}
	chains := make(map[string]realtimeChainHealth, len(h.chains))
	for key, health := range h.chains {
		chains[key] = health
	}
	pending := make([]multiCandidate, 0, len(h.pending))
	for _, c := range h.pending {
		pending = append(pending, c)
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].SeenAt.Before(pending[j].SeenAt) })
	b, err := json.MarshalIndent(realtimeCheckpointFile{Version: realtimeStateVersion, UpdatedAt: now, Chains: chains, Pending: pending}, "", "  ")
	if err != nil {
		return
	}
	tmp := realtimeStatePath() + ".tmp"
	if os.WriteFile(tmp, b, 0600) == nil && replaceFile(tmp, realtimeStatePath()) == nil {
		h.lastSaved = now
	}
}

func (h *freeRealtimeHub) Start() {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.running = cancel, true
	for _, m := range realtimeModules() {
		health := h.chains[m.Key]
		health.Chain, health.Connected, health.LastError = m.Key, false, ""
		h.chains[m.Key] = health
	}
	h.persistLocked(true)
	h.mu.Unlock()
	for _, m := range realtimeModules() {
		m := m
		go h.runChain(ctx, m)
	}
}

func (h *freeRealtimeHub) Stop() {
	h.mu.Lock()
	cancel := h.cancel
	h.cancel, h.running = nil, false
	for key, health := range h.chains {
		health.Connected = false
		h.chains[key] = health
	}
	h.persistLocked(true)
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (h *freeRealtimeHub) snapshot() realtimeSummary {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := realtimeSummary{Expected: len(realtimeModules()), Pending: len(h.pending), Running: h.running}
	now := time.Now()
	for _, m := range realtimeModules() {
		health := h.chains[m.Key]
		if health.Connected && !health.LastMessageAt.IsZero() && now.Sub(health.LastMessageAt) <= realtimeStaleAfter {
			s.Connected++
		}
		s.Chains = append(s.Chains, health)
	}
	return s
}

func (h *freeRealtimeHub) pendingSnapshot() ([]multiCandidate, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rows, receipts := make([]multiCandidate, 0, len(h.pending)), make([]string, 0, len(h.pending))
	for key, c := range h.pending {
		rows, receipts = append(rows, c), append(receipts, key)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].SeenAt.Before(rows[j].SeenAt) })
	return rows, receipts
}

func (h *freeRealtimeHub) acknowledge(receipts []string) {
	if len(receipts) == 0 {
		return
	}
	h.mu.Lock()
	for _, receipt := range receipts {
		delete(h.pending, receipt)
	}
	h.persistLocked(true)
	h.mu.Unlock()
}

func pendingRealtimeCandidates() ([]multiCandidate, []string) { return freeRealtime.pendingSnapshot() }
func ackRealtimeCandidates(receipts []string)                 { freeRealtime.acknowledge(receipts) }
func currentRealtimeSummary() realtimeSummary                 { return freeRealtime.snapshot() }
func startFreeRealtimeDiscovery()                             { freeRealtime.Start() }
func stopFreeRealtimeDiscovery()                              { freeRealtime.Stop() }

func (h *freeRealtimeHub) updateHealth(chain string, mutate func(*realtimeChainHealth), force bool) {
	h.mu.Lock()
	health := h.chains[chain]
	health.Chain = chain
	mutate(&health)
	h.chains[chain] = health
	h.persistLocked(force)
	h.mu.Unlock()
}

func (h *freeRealtimeHub) enqueue(c multiCandidate) {
	if !validAddress(c.TokenAddress) || !validAddress(c.PoolAddress) {
		return
	}
	c.Chain, c.TokenAddress, c.PoolAddress = normalizeChain(c.Chain), strings.ToLower(c.TokenAddress), strings.ToLower(c.PoolAddress)
	c.Realtime = true
	if c.SeenAt.IsZero() {
		c.SeenAt = time.Now()
	}
	key := realtimeCandidateKey(c)
	h.mu.Lock()
	if _, exists := h.pending[key]; exists {
		h.mu.Unlock()
		return
	}
	h.pending[key] = c
	health := h.chains[c.Chain]
	health.Chain, health.LastEventAt, health.LastEventBlock = c.Chain, c.SeenAt, c.BlockNumber
	health.EventCount++
	h.chains[c.Chain] = health
	h.persistLocked(true)
	h.mu.Unlock()
	if app.hwnd != 0 {
		pPostMessageW.Call(uintptr(app.hwnd), WM_REALTIME_EVENT, 0, 0)
	}
}

func realtimeWSEndpoints(m chainModule) []string {
	envName := strings.TrimSuffix(m.EnvRPC, "_RPC_URL") + "_WSS_URL"
	items := []string{}
	if custom := strings.TrimSpace(os.Getenv(envName)); custom != "" {
		items = append(items, custom)
	}
	items = append(items, m.WSRPCs...)
	seen, out := map[string]bool{}, []string{}
	for _, raw := range items {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
			continue
		}
		if !seen[u.String()] {
			seen[u.String()] = true
			out = append(out, u.String())
		}
	}
	return out
}

func waitWithContext(ctx context.Context, delay time.Duration) bool {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (h *freeRealtimeHub) runChain(ctx context.Context, m chainModule) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		endpoints := realtimeWSEndpoints(m)
		if len(endpoints) == 0 {
			h.updateHealth(m.Key, func(v *realtimeChainHealth) { v.Connected, v.LastError = false, "未配置有效 WSS 地址" }, true)
			waitWithContext(ctx, 30*time.Second)
			continue
		}
		for index, endpoint := range endpoints {
			if ctx.Err() != nil {
				return
			}
			err := h.listenEndpoint(ctx, m, endpoint)
			if ctx.Err() != nil {
				return
			}
			h.updateHealth(m.Key, func(v *realtimeChainHealth) {
				v.Connected = false
				v.LastError = shortErr(err)
				if index > 0 {
					v.Failovers++
				}
			}, true)
			if !waitWithContext(ctx, 2*time.Second) {
				return
			}
		}
		if !waitWithContext(ctx, backoff) {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func realtimeLogFilter(m chainModule) map[string]any {
	addresses := make([]string, 0, len(m.Factories))
	topics := make([]string, 0, len(m.Factories))
	for _, factory := range m.Factories {
		addresses, topics = append(addresses, factory.Address), append(topics, factory.Topic)
	}
	return map[string]any{"address": addresses, "topics": []any{topics}}
}

func (h *freeRealtimeHub) listenEndpoint(ctx context.Context, m chainModule, endpoint string) error {
	ws, err := dialRealtimeWebSocket(ctx, endpoint)
	if err != nil {
		return err
	}
	defer ws.Close()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = ws.Close()
		case <-done:
		}
	}()
	defer close(done)

	headSub, logSub, err := ws.subscribe(ctx, realtimeLogFilter(m))
	if err != nil {
		return err
	}
	h.updateHealth(m.Key, func(v *realtimeChainHealth) {
		v.Endpoint, v.Connected, v.LastConnectedAt, v.LastMessageAt, v.LastError = endpoint, true, time.Now(), time.Now(), ""
		v.Reconnects++
	}, true)

	for {
		payload, err := ws.readText()
		if err != nil {
			return err
		}
		now := time.Now()
		h.updateHealth(m.Key, func(v *realtimeChainHealth) { v.LastMessageAt = now }, false)
		var envelope struct {
			Method string `json:"method"`
			Params struct {
				Subscription string          `json:"subscription"`
				Result       json.RawMessage `json:"result"`
			} `json:"params"`
		}
		if json.Unmarshal(payload, &envelope) != nil || envelope.Method != "eth_subscription" {
			continue
		}
		switch envelope.Params.Subscription {
		case headSub:
			var head struct {
				Number    string `json:"number"`
				Timestamp string `json:"timestamp"`
			}
			if json.Unmarshal(envelope.Params.Result, &head) == nil {
				n, _ := parseHexUint(head.Number)
				ts, _ := parseHexUint(head.Timestamp)
				h.updateHealth(m.Key, func(v *realtimeChainHealth) {
					v.LastHead, v.LastHeadAt = n, now
					if ts > 0 {
						v.LastHeadLag = now.Sub(time.Unix(int64(ts), 0))
					}
				}, false)
			}
		case logSub:
			var log rpcLog
			if json.Unmarshal(envelope.Params.Result, &log) != nil || log.Removed {
				continue
			}
			if factory, ok := factoryForRealtimeLog(m, log); ok {
				for _, candidate := range candidatesFromFactoryLog(m, factory, log, now, true) {
					h.enqueue(candidate)
				}
			}
		}
	}
}

// realtimeWS is the small RFC 6455 client needed for JSON-RPC subscriptions.
// Client-to-server frames must be masked; server frames are accepted masked or
// unmasked defensively. Payloads are bounded to protect the desktop process.
type realtimeWS struct {
	conn      net.Conn
	reader    *bufio.Reader
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func dialRealtimeWebSocket(ctx context.Context, rawURL string) (*realtimeWS, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" {
		return nil, errors.New("invalid websocket URL")
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "wss" {
			port = "443"
		} else {
			port = "80"
		}
	}
	address := net.JoinHostPort(u.Hostname(), port)
	dialer := &net.Dialer{Timeout: 12 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "wss" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		conn = tlsConn
	}
	keyRaw := make([]byte, 16)
	if _, err := rand.Read(keyRaw); err != nil {
		_ = conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyRaw)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket upgrade returned HTTP %d", resp.StatusCode)
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		_ = conn.Close()
		return nil, errors.New("websocket accept proof is invalid")
	}
	_ = conn.SetDeadline(time.Time{})
	return &realtimeWS{conn: conn, reader: reader}, nil
}

func (c *realtimeWS) Close() error {
	var err error
	c.closeOnce.Do(func() { err = c.conn.Close() })
	return err
}

func (c *realtimeWS) subscribe(ctx context.Context, logFilter map[string]any) (string, string, error) {
	if err := c.writeJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "eth_subscribe", "params": []any{"newHeads"}}); err != nil {
		return "", "", err
	}
	if err := c.writeJSON(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "eth_subscribe", "params": []any{"logs", logFilter}}); err != nil {
		return "", "", err
	}
	headSub, logSub := "", ""
	for headSub == "" || logSub == "" {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		payload, err := c.readText()
		if err != nil {
			return "", "", err
		}
		var response struct {
			ID     json.RawMessage `json:"id"`
			Result string          `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(payload, &response) != nil || len(response.ID) == 0 {
			continue
		}
		if response.Error != nil {
			return "", "", fmt.Errorf("subscription rejected (%d): %s", response.Error.Code, response.Error.Message)
		}
		var id int
		if json.Unmarshal(response.ID, &id) != nil || response.Result == "" {
			continue
		}
		switch id {
		case 1:
			headSub = response.Result
		case 2:
			logSub = response.Result
		}
	}
	return headSub, logSub, nil
}

func (c *realtimeWS) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeFrame(0x1, b)
}

func (c *realtimeWS) writeFrame(opcode byte, payload []byte) error {
	if len(payload) > realtimeMaxPayload {
		return errors.New("websocket payload too large")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	header := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, 0x80|byte(n))
	case n <= 0xffff:
		header = append(header, 0x80|126, byte(n>>8), byte(n))
	default:
		header = append(header, 0x80|127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)
	masked := append([]byte(nil), payload...)
	for i := range masked {
		masked[i] ^= mask[i%4]
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.conn.Write(append(header, masked...))
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

func (c *realtimeWS) readText() ([]byte, error) {
	var assembled []byte
	started := false
	for {
		opcode, final, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x8:
			_ = c.writeFrame(0x8, nil)
			return nil, io.EOF
		case 0x9:
			if err := c.writeFrame(0xA, payload); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		case 0x1:
			if started {
				return nil, errors.New("unexpected websocket text frame")
			}
			assembled, started = append(assembled, payload...), true
		case 0x0:
			if !started {
				return nil, errors.New("unexpected websocket continuation")
			}
			assembled = append(assembled, payload...)
		default:
			return nil, fmt.Errorf("unsupported websocket opcode %d", opcode)
		}
		if len(assembled) > realtimeMaxPayload {
			return nil, errors.New("websocket message too large")
		}
		if final && started {
			return assembled, nil
		}
	}
}

func (c *realtimeWS) readFrame() (byte, bool, []byte, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(realtimeStaleAfter))
	first, err := c.reader.ReadByte()
	if err != nil {
		return 0, false, nil, err
	}
	second, err := c.reader.ReadByte()
	if err != nil {
		return 0, false, nil, err
	}
	if first&0x70 != 0 {
		return 0, false, nil, errors.New("websocket extensions are not supported")
	}
	final, opcode, masked := first&0x80 != 0, first&0x0f, second&0x80 != 0
	length := uint64(second & 0x7f)
	switch length {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(c.reader, b[:]); err != nil {
			return 0, false, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(c.reader, b[:]); err != nil {
			return 0, false, nil, err
		}
		length = binary.BigEndian.Uint64(b[:])
	}
	if length > realtimeMaxPayload {
		return 0, false, nil, errors.New("websocket frame too large")
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
			return 0, false, nil, err
		}
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return 0, false, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	if opcode >= 0x8 && (!final || length > 125) {
		return 0, false, nil, errors.New("invalid websocket control frame")
	}
	return opcode, final, payload, nil
}
