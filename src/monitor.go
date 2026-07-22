package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	monitorVersion          = 1
	monitorSnapshotInterval = 30 * time.Second
	monitorAlertCooldown    = 10 * time.Minute
	maxSnapshotsPerToken    = 2880
	maxMonitorAlerts        = 2000
)

type WatchEntry struct {
	Chain   string    `json:"chain"`
	Address string    `json:"address"`
	Symbol  string    `json:"symbol"`
	Name    string    `json:"name"`
	AddedAt time.Time `json:"added_at"`
}

type MonitorSnapshot struct {
	ObservedAt time.Time `json:"observed_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Chain      string    `json:"chain"`
	Address    string    `json:"address"`
	Pool       string    `json:"pool"`
	Symbol     string    `json:"symbol"`
	Name       string    `json:"name"`
	Price      float64   `json:"price"`
	Liquidity  float64   `json:"liquidity"`
	Volume24   float64   `json:"volume_24h"`
	Score      int       `json:"score"`
	Security   string    `json:"security"`
	Source     string    `json:"source"`
	Block      uint64    `json:"block"`
}

type MonitorAlert struct {
	Time    time.Time `json:"time"`
	Key     string    `json:"key"`
	Level   string    `json:"level"`
	Kind    string    `json:"kind"`
	Symbol  string    `json:"symbol"`
	Message string    `json:"message"`
}

type MonitorState struct {
	Version   int                          `json:"version"`
	Watch     map[string]WatchEntry        `json:"watch"`
	Snapshots map[string][]MonitorSnapshot `json:"snapshots"`
	Alerts    []MonitorAlert               `json:"alerts"`
}

func NewMonitorState() *MonitorState {
	m := &MonitorState{Version: monitorVersion}
	m.Normalize()
	return m
}

func (m *MonitorState) Normalize() {
	if m.Version <= 0 {
		m.Version = monitorVersion
	}
	if m.Watch == nil {
		m.Watch = map[string]WatchEntry{}
	}
	if m.Snapshots == nil {
		m.Snapshots = map[string][]MonitorSnapshot{}
	}
	if m.Alerts == nil {
		m.Alerts = []MonitorAlert{}
	}
	if len(m.Alerts) > maxMonitorAlerts {
		m.Alerts = append([]MonitorAlert(nil), m.Alerts[len(m.Alerts)-maxMonitorAlerts:]...)
	}
}

func monitorIdentity(chain, address string) string {
	return tokenIdentity(normalizeChain(chain), normalizeAddress(address))
}

func (m *MonitorState) IsWatching(t Token) bool {
	if m == nil {
		return false
	}
	_, ok := m.Watch[monitorIdentity(t.Chain, t.Address)]
	return ok
}

func (m *MonitorState) Count() int {
	if m == nil {
		return 0
	}
	return len(m.Watch)
}

func (m *MonitorState) WatchedIdentities() []string {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m.Watch))
	for key := range m.Watch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Toggle returns true when the token is now watched.
func (m *MonitorState) Toggle(t Token, now time.Time) bool {
	m.Normalize()
	key := monitorIdentity(t.Chain, t.Address)
	if _, ok := m.Watch[key]; ok {
		delete(m.Watch, key)
		return false
	}
	m.Watch[key] = WatchEntry{
		Chain: normalizeChain(t.Chain), Address: normalizeAddress(t.Address),
		Symbol: t.Symbol, Name: t.Name, AddedAt: now,
	}
	m.appendSnapshot(key, snapshotFromToken(t, now), true)
	m.addAlert(MonitorAlert{Time: now, Key: key, Level: "info", Kind: "watch-added", Symbol: t.Symbol, Message: "已加入可信监控"})
	return true
}

func snapshotFromToken(t Token, now time.Time) MonitorSnapshot {
	return MonitorSnapshot{
		ObservedAt: now, UpdatedAt: t.UpdatedAt, Chain: normalizeChain(t.Chain),
		Address: normalizeAddress(t.Address), Pool: t.PoolAddress, Symbol: t.Symbol,
		Name: t.Name, Price: t.Price, Liquidity: t.Liquidity, Volume24: t.Volume24,
		Score: t.Score, Security: t.Security, Source: t.Source, Block: t.BlockNumber,
	}
}

func (m *MonitorState) appendSnapshot(key string, snap MonitorSnapshot, force bool) {
	rows := m.Snapshots[key]
	if !force && len(rows) > 0 && snap.ObservedAt.Sub(rows[len(rows)-1].ObservedAt) < monitorSnapshotInterval {
		return
	}
	rows = append(rows, snap)
	if len(rows) > maxSnapshotsPerToken {
		rows = append([]MonitorSnapshot(nil), rows[len(rows)-maxSnapshotsPerToken:]...)
	}
	m.Snapshots[key] = rows
}

func (m *MonitorState) addAlert(alert MonitorAlert) {
	for i := len(m.Alerts) - 1; i >= 0; i-- {
		previous := m.Alerts[i]
		if previous.Key == alert.Key && previous.Kind == alert.Kind {
			if alert.Time.Sub(previous.Time) < monitorAlertCooldown {
				return
			}
			break
		}
	}
	m.Alerts = append(m.Alerts, alert)
	if len(m.Alerts) > maxMonitorAlerts {
		m.Alerts = append([]MonitorAlert(nil), m.Alerts[len(m.Alerts)-maxMonitorAlerts:]...)
	}
}

func (m *MonitorState) Observe(tokens []Token, now time.Time) []MonitorAlert {
	m.Normalize()
	before := len(m.Alerts)
	current := make(map[string]Token, len(tokens))
	for _, t := range tokens {
		current[monitorIdentity(t.Chain, t.Address)] = t
	}
	for key, watch := range m.Watch {
		t, ok := current[key]
		if !ok {
			m.addAlert(MonitorAlert{Time: now, Key: key, Level: "warning", Kind: "data-missing", Symbol: watch.Symbol, Message: "本轮没有返回该关注项的数据"})
			continue
		}
		rows := m.Snapshots[key]
		var previous MonitorSnapshot
		if len(rows) > 0 {
			previous = rows[len(rows)-1]
		}
		if previous.Price > 0 && t.Price > 0 {
			change := (t.Price - previous.Price) / previous.Price * 100
			if math.Abs(change) >= 10 {
				level := "warning"
				if change < 0 {
					level = "critical"
				}
				m.addAlert(MonitorAlert{Time: now, Key: key, Level: level, Kind: "price-move", Symbol: t.Symbol, Message: fmt.Sprintf("价格相对上次快照变化 %+.1f%%", change)})
			}
		}
		if previous.Liquidity > 0 && t.Liquidity > 0 && t.Liquidity < previous.Liquidity*0.8 {
			drop := (previous.Liquidity - t.Liquidity) / previous.Liquidity * 100
			m.addAlert(MonitorAlert{Time: now, Key: key, Level: "critical", Kind: "liquidity-drop", Symbol: t.Symbol, Message: fmt.Sprintf("流动性下降 %.1f%%", drop)})
		}
		if previous.Security != "" && t.Security != previous.Security {
			m.addAlert(MonitorAlert{Time: now, Key: key, Level: "critical", Kind: "security-change", Symbol: t.Symbol, Message: fmt.Sprintf("安全状态从“%s”变为“%s”", previous.Security, t.Security)})
		}
		if previous.Score > 0 && previous.Score-t.Score >= 15 {
			m.addAlert(MonitorAlert{Time: now, Key: key, Level: "warning", Kind: "score-drop", Symbol: t.Symbol, Message: fmt.Sprintf("质量分从 %d 降至 %d", previous.Score, t.Score)})
		}
		if !t.UpdatedAt.IsZero() && now.Sub(t.UpdatedAt) > 5*time.Minute {
			m.addAlert(MonitorAlert{Time: now, Key: key, Level: "warning", Kind: "stale-data", Symbol: t.Symbol, Message: "上游数据已超过 5 分钟未更新"})
		}
		m.appendSnapshot(key, snapshotFromToken(t, now), false)
	}
	if before >= len(m.Alerts) {
		return nil
	}
	return append([]MonitorAlert(nil), m.Alerts[before:]...)
}

func monitorStatePath() string { return filepath.Join(dataDir(), "trusted_monitor_v27.json") }

func loadMonitorState() *MonitorState {
	b, err := os.ReadFile(monitorStatePath())
	if err != nil {
		return NewMonitorState()
	}
	var state MonitorState
	if json.Unmarshal(b, &state) != nil {
		return NewMonitorState()
	}
	state.Normalize()
	return &state
}

func saveMonitorState(state *MonitorState) error {
	if state == nil {
		return nil
	}
	state.Normalize()
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := monitorStatePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return replaceFile(tmp, monitorStatePath())
}

func (m *MonitorState) ExportCSV(path string) error {
	if m == nil || (len(m.Snapshots) == 0 && len(m.Alerts) == 0) {
		return errors.New("暂无可信监控记录")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, _ = f.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(f)
	_ = w.Write([]string{"记录类型", "时间", "链", "代币", "合约", "价格", "流动性", "24H成交", "质量分", "安全状态", "来源", "数据更新时间", "区块", "告警级别", "告警类型", "说明"})
	keys := make([]string, 0, len(m.Snapshots))
	for key := range m.Snapshots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, row := range m.Snapshots[key] {
			_ = w.Write([]string{"快照", row.ObservedAt.Format(time.RFC3339), row.Chain, row.Symbol, row.Address, strconv.FormatFloat(row.Price, 'g', -1, 64), strconv.FormatFloat(row.Liquidity, 'f', 2, 64), strconv.FormatFloat(row.Volume24, 'f', 2, 64), strconv.Itoa(row.Score), row.Security, row.Source, row.UpdatedAt.Format(time.RFC3339), strconv.FormatUint(row.Block, 10), "", "", ""})
		}
	}
	for _, alert := range m.Alerts {
		watch := m.Watch[alert.Key]
		_ = w.Write([]string{"告警", alert.Time.Format(time.RFC3339), watch.Chain, alert.Symbol, watch.Address, "", "", "", "", "", "", "", "", alert.Level, alert.Kind, alert.Message})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return f.Sync()
}

func latestMonitorAlertText(state *MonitorState) string {
	if state == nil || len(state.Alerts) == 0 {
		return "暂无异常告警"
	}
	alert := state.Alerts[len(state.Alerts)-1]
	return strings.TrimSpace(alert.Symbol + " · " + alert.Message)
}

func toggleSelectedWatch() {
	if app.selected < 0 || app.selected >= len(app.results) {
		return
	}
	if app.monitor == nil {
		app.monitor = NewMonitorState()
	}
	token := app.results[app.selected]
	watching := app.monitor.Toggle(token, time.Now())
	if err := saveMonitorState(app.monitor); err != nil {
		app.toast = "关注列表保存失败：" + shortErr(err)
		app.toastUntil = time.Now().Add(5 * time.Second)
		invalidate(false)
		return
	}
	if watching {
		app.toast = token.Symbol + " 已加入可信监控"
		addLog("可信监控：已关注 " + token.Symbol + "（" + chainLabel(token.Chain) + "）")
	} else {
		app.toast = token.Symbol + " 已取消关注"
		addLog("可信监控：已取消关注 " + token.Symbol)
		if app.filterMode == 1 {
			selectFirstVisible(buildLayout())
		}
	}
	app.toastUntil = time.Now().Add(4 * time.Second)
	invalidate(false)
}

func exportMonitorReport() {
	if app.monitor == nil || app.monitor.Count() == 0 {
		return
	}
	home, _ := os.UserHomeDir()
	desktop := filepath.Join(home, "Desktop")
	if _, err := os.Stat(desktop); err != nil {
		desktop = home
	}
	path := filepath.Join(desktop, "MultiChainRadar_TrustedMonitor_"+time.Now().Format("20060102_150405")+".csv")
	if err := app.monitor.ExportCSV(path); err != nil {
		app.toast = "监控报告导出失败：" + shortErr(err)
		app.toastUntil = time.Now().Add(5 * time.Second)
		invalidate(false)
		return
	}
	app.toast = "可信监控报告已导出到桌面"
	app.toastUntil = time.Now().Add(4 * time.Second)
	addLog("可信监控报告已导出：" + path)
	invalidate(false)
}
