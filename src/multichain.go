//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// V2.5 keeps EVM chains behind one small module contract.  Adding a chain no
// longer requires copying the full scanner or mixing its cursor with another
// network.
type chainModule struct {
	Key             string
	Name            string
	Short           string
	ChainID         string
	DexSlug         string
	EnvRPC          string
	RPCs            []string
	Factories       []factorySpec
	KnownAssets     map[string]bool
	InitialLookback uint64
	ChunkSize       uint64
}

const (
	pancakeV2BSC = "0xca143ce32fe78f1f7019d7d551a6402fc5350c73"
	pancakeV2EVM = "0x02a84c1b3bbd7401a5f7fa98a384ebc70bb5749e"
	pancakeV3EVM = "0x0bfbcf9fa4f9c56b0f40a671ad40e0805a091865"
)

var chainModules = []chainModule{
	{
		Key: "base", Name: "Base", Short: "BASE", ChainID: "8453", DexSlug: "base", EnvRPC: "BASE_RPC_URL",
		RPCs: []string{"https://mainnet.base.org", "https://mainnet-preconf.base.org"},
		Factories: []factorySpec{
			{Name: "Aerodrome", Address: aerodromeFactory, Topic: topicAerodromePoolCreated, Kind: "aero"},
			{Name: "Uniswap V3", Address: uniswapV3Factory, Topic: topicUniswapV3PoolCreated, Kind: "v3"},
			{Name: "Uniswap V2", Address: uniswapV2Factory, Topic: topicUniswapV2PairCreated, Kind: "v2"},
			{Name: "Pancake V2", Address: pancakeV2EVM, Topic: topicUniswapV2PairCreated, Kind: "v2"},
			{Name: "Pancake V3", Address: pancakeV3EVM, Topic: topicUniswapV3PoolCreated, Kind: "v3"},
		},
		KnownAssets: map[string]bool{
			"0x4200000000000000000000000000000000000006": true,
			"0x833589fcd6edb6e08f4c7c32d4f71b54bda02913": true,
			"0xd9aa3213c4d10a4f6e06a01d9a62f3ecf8a3f6f2": true,
			"0xcbb7c0000ab88b473b1f5afd9ef808440eed33bf": true,
			"0x50c5725949a6f0c72e6c4a641f24049a917db0cb": true,
			"0x940181a94a35a4569e4529a3cdfb74e38fd98631": true,
		}, InitialLookback: 1800, ChunkSize: 500,
	},
	{
		Key: "bsc", Name: "BNB Smart Chain", Short: "BSC", ChainID: "56", DexSlug: "bsc", EnvRPC: "BSC_RPC_URL",
		// BNB Chain's no-key dataseed endpoints disable eth_getLogs.  PublicNode
		// and SubQuery are therefore tried first; users can override with BSC_RPC_URL.
		RPCs: []string{"https://bsc-rpc.publicnode.com", "https://bnb.rpc.subquery.network/public"},
		Factories: []factorySpec{
			{Name: "Pancake V2", Address: pancakeV2BSC, Topic: topicUniswapV2PairCreated, Kind: "v2"},
			{Name: "Pancake V3", Address: pancakeV3EVM, Topic: topicUniswapV3PoolCreated, Kind: "v3"},
		},
		KnownAssets: map[string]bool{
			"0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c": true,
			"0x55d398326f99059ff775485246999027b3197955": true,
			"0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d": true,
			"0xc5f0f7b66764f6ec8c8dff7ba683102295e16409": true,
			"0x2170ed0880ac9a755fd29b2688956bd959f933f8": true,
			"0x0e09fabb73bd3ade0a17ecc321fd13a19e81ce82": true,
		}, InitialLookback: 1500, ChunkSize: 250,
	},
	{
		Key: "arbitrum", Name: "Arbitrum One", Short: "ARB", ChainID: "42161", DexSlug: "arbitrum", EnvRPC: "ARBITRUM_RPC_URL",
		RPCs: []string{"https://arb1.arbitrum.io/rpc", "https://arbitrum-one-rpc.publicnode.com"},
		Factories: []factorySpec{
			{Name: "Pancake V2", Address: pancakeV2EVM, Topic: topicUniswapV2PairCreated, Kind: "v2"},
			{Name: "Pancake V3", Address: pancakeV3EVM, Topic: topicUniswapV3PoolCreated, Kind: "v3"},
		},
		KnownAssets: map[string]bool{
			"0x82af49447d8a07e3bd95bd0d56f35241523fbab1": true,
			"0xaf88d065e77c8cc2239327c5edb3a432268e5831": true,
			"0xff970a61a04b1ca14834a43f5de4533ebddb5cc8": true,
			"0xfd086bc7cd5c481dcc9c85ebe478a1c0b69fcbb9": true,
			"0x2f2a2543b76a4166549f7aab2e75bef0aefc5b0f": true,
			"0x912ce59144191c1204e64559fe8253a0e49e6548": true,
		}, InitialLookback: 2400, ChunkSize: 400,
	},
}

func chainByKey(key string) (chainModule, bool) {
	key = normalizeChain(key)
	for _, m := range chainModules {
		if m.Key == key {
			return m, true
		}
	}
	return chainModule{}, false
}

func chainLabel(key string) string {
	if m, ok := chainByKey(key); ok {
		return m.Short
	}
	return strings.ToUpper(key)
}

func tokenIdentity(chain, address string) string {
	return normalizeChain(chain) + "|" + strings.ToLower(strings.TrimSpace(address))
}

type multiCandidate struct {
	Chain        string    `json:"chain"`
	TokenAddress string    `json:"token_address"`
	PoolAddress  string    `json:"pool_address"`
	Factory      string    `json:"factory"`
	BlockNumber  uint64    `json:"block_number"`
	SeenAt       time.Time `json:"seen_at"`
}

type multiCandidateFile struct {
	Version    int               `json:"version"`
	LastBlocks map[string]uint64 `json:"last_blocks"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Candidates []multiCandidate  `json:"candidates"`
}

func multiCandidatePath() string { return filepath.Join(dataDir(), "multichain_candidates_v25.json") }

func loadMultiCandidates() multiCandidateFile {
	f := multiCandidateFile{Version: 1, LastBlocks: map[string]uint64{}}
	if b, err := os.ReadFile(multiCandidatePath()); err == nil {
		_ = json.Unmarshal(b, &f)
	} else if b, e := os.ReadFile(filepath.Join(dataDir(), "chain_candidates_v24.json")); e == nil {
		// Migrate the Base-only cursor/candidates exactly once.
		var old chainCandidateFile
		if json.Unmarshal(b, &old) == nil {
			f.LastBlocks["base"] = old.LastBlock
			for _, c := range old.Candidates {
				f.Candidates = append(f.Candidates, multiCandidate{Chain: "base", TokenAddress: c.TokenAddress, PoolAddress: c.PoolAddress, Factory: c.Factory, BlockNumber: c.BlockNumber, SeenAt: c.SeenAt})
			}
		}
	}
	if f.LastBlocks == nil {
		f.LastBlocks = map[string]uint64{}
	}
	cutoff := time.Now().Add(-48 * time.Hour)
	out := make([]multiCandidate, 0, len(f.Candidates))
	for _, c := range f.Candidates {
		c.Chain = normalizeChain(c.Chain)
		c.TokenAddress = strings.ToLower(c.TokenAddress)
		c.PoolAddress = strings.ToLower(c.PoolAddress)
		if c.SeenAt.After(cutoff) && validAddress(c.TokenAddress) {
			out = append(out, c)
		}
	}
	f.Candidates = out
	return f
}

func saveMultiCandidates(f multiCandidateFile) {
	f.Version = 1
	f.UpdatedAt = time.Now()
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	tmp := multiCandidatePath() + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		_ = replaceFile(tmp, multiCandidatePath())
	}
}

func rpcCallModule(ctx context.Context, m chainModule, method string, params any, out any) (string, string, error) {
	endpoints := make([]string, 0, len(m.RPCs)+1)
	if custom := strings.TrimSpace(os.Getenv(m.EnvRPC)); custom != "" {
		endpoints = append(endpoints, custom)
	}
	endpoints = append(endpoints, m.RPCs...)
	var errs []string
	for _, ep := range endpoints {
		engine, err := rpcCallEndpoint(ctx, ep, method, params, out)
		if err == nil {
			return ep, engine, nil
		}
		errs = append(errs, ep+"："+shortErr(err))
		if ctx.Err() != nil {
			break
		}
	}
	return "", "", errors.New(strings.Join(errs, "；"))
}

func scanFactoryLogsModule(ctx context.Context, m chainModule, from, to uint64, spec factorySpec) ([]multiCandidate, error) {
	filter := map[string]any{"fromBlock": fmt.Sprintf("0x%x", from), "toBlock": fmt.Sprintf("0x%x", to), "address": spec.Address, "topics": []any{spec.Topic}}
	var logs []rpcLog
	_, _, err := rpcCallModule(ctx, m, "eth_getLogs", []any{filter}, &logs)
	if err != nil {
		return nil, err
	}
	out := []multiCandidate{}
	for _, lg := range logs {
		if lg.Removed || len(lg.Topics) < 3 {
			continue
		}
		t0, t1 := topicAddress(lg.Topics[1]), topicAddress(lg.Topics[2])
		if !validAddress(t0) || !validAddress(t1) {
			continue
		}
		pool := ""
		if spec.Kind == "v3" {
			pool = dataAddress(lg.Data, 1)
		} else {
			pool = dataAddress(lg.Data, 0)
		}
		if !validAddress(pool) {
			continue
		}
		bn, _ := parseHexUint(lg.BlockNumber)
		add := func(tok string) {
			tok = strings.ToLower(tok)
			if !m.KnownAssets[tok] {
				out = append(out, multiCandidate{Chain: m.Key, TokenAddress: tok, PoolAddress: pool, Factory: spec.Name, BlockNumber: bn, SeenAt: time.Now()})
			}
		}
		if m.KnownAssets[t0] && !m.KnownAssets[t1] {
			add(t1)
		} else if m.KnownAssets[t1] && !m.KnownAssets[t0] {
			add(t0)
		} else {
			add(t0)
			add(t1)
		}
	}
	return out, nil
}

func scanModuleRange(ctx context.Context, m chainModule, from, to uint64) ([]multiCandidate, []string, uint64, bool) {
	found := []multiCandidate{}
	logs := []string{}
	completed := uint64(0)
	if from > 0 {
		completed = from - 1
	}
	chunk := m.ChunkSize
	if chunk == 0 {
		chunk = 300
	}
	for start := from; start <= to; {
		end := start + chunk - 1
		if end > to {
			end = to
		}
		ok := true
		for _, factory := range m.Factories {
			cs, err := scanFactoryLogsModule(ctx, m, start, end, factory)
			if err != nil {
				ok = false
				logs = append(logs, fmt.Sprintf("%s/%s 区块 %d-%d 失败：%s", m.Short, factory.Name, start, end, shortErr(err)))
				continue
			}
			if len(cs) > 0 {
				logs = append(logs, fmt.Sprintf("%s/%s：发现 %d 个候选", m.Short, factory.Name, len(cs)))
			}
			found = append(found, cs...)
		}
		if !ok {
			return found, logs, completed, false
		}
		completed = end
		if end == to {
			break
		}
		start = end + 1
	}
	return found, logs, completed, true
}

type moduleDiscoverResult struct {
	Module     chainModule
	Candidates []multiCandidate
	Logs       []string
	Latest     uint64
	NewFound   int
	LastBlock  uint64
	Err        error
}

func discoverModule(ctx context.Context, m chainModule, last uint64) moduleDiscoverResult {
	var latestHex string
	ep, engine, err := rpcCallModule(ctx, m, "eth_blockNumber", []any{}, &latestHex)
	if err != nil {
		return moduleDiscoverResult{Module: m, LastBlock: last, Err: fmt.Errorf("%s RPC：%w", m.Short, err)}
	}
	latest, err := parseHexUint(latestHex)
	if err != nil || latest == 0 {
		return moduleDiscoverResult{Module: m, LastBlock: last, Err: fmt.Errorf("%s RPC 返回无效区块", m.Short)}
	}
	logs := []string{fmt.Sprintf("%s RPC：区块 %d，%s，%s", m.Short, latest, engine, ep)}
	from := last + 1
	if last == 0 || (from <= latest && latest-from > m.InitialLookback*2) {
		if latest > m.InitialLookback {
			from = latest - m.InitialLookback
		} else {
			from = 0
		}
	}
	if from > latest {
		return moduleDiscoverResult{Module: m, Logs: logs, Latest: latest, LastBlock: last}
	}
	found, more, completed, complete := scanModuleRange(ctx, m, from, latest)
	logs = append(logs, more...)
	newLast := last
	if complete {
		newLast = latest
	} else if completed >= from {
		newLast = completed
	}
	logs = append(logs, fmt.Sprintf("%s：新发现 %d 条，游标 %d", m.Short, len(found), newLast))
	return moduleDiscoverResult{Module: m, Candidates: found, Logs: logs, Latest: latest, NewFound: len(found), LastBlock: newLast}
}

func discoverAllModules(ctx context.Context) ([]multiCandidate, []string, map[string]uint64, int, int, error) {
	f := loadMultiCandidates()
	ch := make(chan moduleDiscoverResult, len(chainModules))
	var wg sync.WaitGroup
	for _, m := range chainModules {
		m := m
		wg.Add(1)
		go func() { defer wg.Done(); ch <- discoverModule(ctx, m, f.LastBlocks[m.Key]) }()
	}
	wg.Wait()
	close(ch)
	logs := []string{}
	latest := map[string]uint64{}
	newFound := 0
	active := 0
	var errs []string
	found := []multiCandidate{}
	for r := range ch {
		logs = append(logs, r.Logs...)
		if r.Err != nil {
			logs = append(logs, "链模块不可用："+shortErr(r.Err))
			errs = append(errs, r.Err.Error())
			continue
		}
		active++
		latest[r.Module.Key] = r.Latest
		f.LastBlocks[r.Module.Key] = r.LastBlock
		newFound += r.NewFound
		found = append(found, r.Candidates...)
	}
	merged := map[string]multiCandidate{}
	for _, c := range append(f.Candidates, found...) {
		c.Chain = normalizeChain(c.Chain)
		c.TokenAddress = strings.ToLower(c.TokenAddress)
		c.PoolAddress = strings.ToLower(c.PoolAddress)
		k := tokenIdentity(c.Chain, c.TokenAddress)
		if old, ok := merged[k]; !ok || c.BlockNumber > old.BlockNumber {
			merged[k] = c
		}
	}
	cutoff := time.Now().Add(-48 * time.Hour)
	perChain := map[string][]multiCandidate{}
	for _, c := range merged {
		if c.SeenAt.After(cutoff) {
			perChain[c.Chain] = append(perChain[c.Chain], c)
		}
	}
	all := []multiCandidate{}
	for _, m := range chainModules {
		rows := perChain[m.Key]
		sort.Slice(rows, func(i, j int) bool { return rows[i].BlockNumber > rows[j].BlockNumber })
		if len(rows) > 80 {
			rows = rows[:80]
		}
		all = append(all, rows...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].SeenAt.After(all[j].SeenAt) })
	f.Candidates = all
	saveMultiCandidates(f)
	logs = append(logs, fmt.Sprintf("多链汇总：本轮新发现 %d 条；本地候选 %d 条；可用链 %d/%d", newFound, len(all), active, len(chainModules)))
	if active == 0 {
		return nil, logs, latest, newFound, active, errors.New(strings.Join(errs, "；"))
	}
	return all, logs, latest, newFound, active, nil
}

type multiRotationFile struct {
	Cursors map[string]int `json:"cursors"`
}

func multiRotationPath() string { return filepath.Join(dataDir(), "rotation_v25.json") }
func loadMultiRotation() multiRotationFile {
	r := multiRotationFile{Cursors: map[string]int{}}
	if b, e := os.ReadFile(multiRotationPath()); e == nil {
		_ = json.Unmarshal(b, &r)
	}
	if r.Cursors == nil {
		r.Cursors = map[string]int{}
	}
	return r
}
func saveMultiRotation(r multiRotationFile) {
	b, _ := json.Marshal(r)
	_ = os.WriteFile(multiRotationPath(), b, 0644)
}

func chooseMultiBatch(all []multiCandidate, perChainLimit int) []multiCandidate {
	r := loadMultiRotation()
	out := []multiCandidate{}
	for _, m := range chainModules {
		rows := []multiCandidate{}
		for _, c := range all {
			if c.Chain == m.Key {
				rows = append(rows, c)
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].BlockNumber > rows[j].BlockNumber })
		if len(rows) <= perChainLimit {
			out = append(out, rows...)
			continue
		}
		priority := 5
		if priority > perChainLimit {
			priority = perChainLimit
		}
		out = append(out, rows[:priority]...)
		rest := rows[priority:]
		cursor := r.Cursors[m.Key]
		if len(rest) > 0 {
			cursor %= len(rest)
		}
		need := perChainLimit - priority
		for i := 0; i < need && i < len(rest); i++ {
			out = append(out, rest[(cursor+i)%len(rest)])
		}
		if len(rest) > 0 {
			r.Cursors[m.Key] = (cursor + need) % len(rest)
		}
	}
	saveMultiRotation(r)
	return out
}

type multiMarketCache struct {
	SavedAt time.Time `json:"saved_at"`
	Tokens  []Token   `json:"tokens"`
}

func multiMarketCachePath() string { return filepath.Join(dataDir(), "market_tokens_v25.json") }
func loadMultiMarketCache() map[string]Token {
	out := map[string]Token{}
	var cf multiMarketCache
	if b, e := os.ReadFile(multiMarketCachePath()); e == nil {
		_ = json.Unmarshal(b, &cf)
	} else if b, e := os.ReadFile(filepath.Join(dataDir(), "market_tokens_v24.json")); e == nil {
		var old marketTokenCache
		if json.Unmarshal(b, &old) == nil {
			for i := range old.Tokens {
				if old.Tokens[i].Chain == "" {
					old.Tokens[i].Chain = "base"
				}
				cf.Tokens = append(cf.Tokens, old.Tokens[i])
			}
		}
	}
	for _, t := range cf.Tokens {
		t.Chain = normalizeChain(t.Chain)
		t.ChainID = chainIDFor(t.Chain)
		t.Address = strings.ToLower(t.Address)
		if validAddress(t.Address) {
			out[tokenIdentity(t.Chain, t.Address)] = t
		}
	}
	return out
}
func saveMultiMarketCache(cache map[string]Token) {
	rows := make([]Token, 0, len(cache))
	for _, t := range cache {
		rows = append(rows, t)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].Score > rows[j].Score
		}
		return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
	})
	if len(rows) > 240 {
		rows = rows[:240]
	}
	b, e := json.MarshalIndent(multiMarketCache{time.Now(), rows}, "", "  ")
	if e != nil {
		return
	}
	tmp := multiMarketCachePath() + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		_ = replaceFile(tmp, multiMarketCachePath())
	}
}

func placeholderMulti(c multiCandidate) Token {
	a := strings.ToLower(c.TokenAddress)
	sym := "NEW"
	if len(a) >= 8 {
		sym = strings.ToUpper(a[2:8])
	}
	label := chainLabel(c.Chain)
	return Token{Chain: c.Chain, ChainID: chainIDFor(c.Chain), Score: 0, Grade: "等待分析", Name: label + " 链上新代币", Symbol: sym, Address: a, AgeHours: time.Since(c.SeenAt).Hours(), Security: "待检测", Source: label + " · " + c.Factory, PoolAddress: c.PoolAddress, BlockNumber: c.BlockNumber, DEXURL: dexURLFor(c.Chain, c.PoolAddress), Evidence: []string{fmt.Sprintf("%s 直接发现新池：%s，区块 %d", label, c.Factory, c.BlockNumber), "等待进入分批分析队列；不是买入信号"}}
}
func chainIDFor(key string) string {
	if m, ok := chainByKey(key); ok {
		return m.ChainID
	}
	return ""
}
func dexURLFor(chain, pool string) string {
	if m, ok := chainByKey(chain); ok {
		return "https://dexscreener.com/" + m.DexSlug + "/" + pool
	}
	return ""
}

func tokenCallStringModule(ctx context.Context, m chainModule, address, selector string) string {
	var result string
	call := map[string]any{"to": address, "data": selector}
	if _, _, e := rpcCallModule(ctx, m, "eth_call", []any{call, "latest"}, &result); e != nil {
		return ""
	}
	return decodeABIString(result)
}
func tokenMetadataModule(ctx context.Context, m chainModule, address string) (string, string) {
	return tokenCallStringModule(ctx, m, address, "0x06fdde03"), tokenCallStringModule(ctx, m, address, "0x95d89b41")
}

func fetchSecurityForChain(ctx context.Context, m chainModule, addresses []string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	missing := []string{}
	now := time.Now()
	securityCacheMu.Lock()
	for _, raw := range addresses {
		a := strings.ToLower(raw)
		k := tokenIdentity(m.Key, a)
		if e, ok := securityCache[k]; ok && now.Sub(e.At) < 30*time.Minute && e.Data != nil {
			out[a] = e.Data
		} else {
			missing = append(missing, a)
		}
	}
	securityCacheMu.Unlock()
	if len(missing) == 0 {
		return out, nil
	}
	var resp struct {
		Code    int                       `json:"code"`
		Message string                    `json:"message"`
		Result  map[string]map[string]any `json:"result"`
	}
	u := "https://api.gopluslabs.io/api/v1/token_security/" + m.ChainID + "?contract_addresses=" + url.QueryEscape(strings.Join(missing, ","))
	if e := getJSON(ctx, u, &resp); e != nil {
		return out, e
	}
	if resp.Code != 1 || resp.Result == nil {
		return out, fmt.Errorf("GoPlus %s 业务错误：code=%d message=%s", m.Short, resp.Code, resp.Message)
	}
	securityCacheMu.Lock()
	for k, v := range resp.Result {
		a := strings.ToLower(k)
		out[a] = v
		securityCache[tokenIdentity(m.Key, a)] = securityCacheEntry{Data: v, At: now}
	}
	securityCacheMu.Unlock()
	return out, nil
}

func topMultiDisplay(cache map[string]Token, limit int) []Token {
	rows := make([]Token, 0, len(cache))
	for _, t := range cache {
		rows = append(rows, t)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Score == rows[j].Score {
			return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
		}
		return rows[i].Score > rows[j].Score
	})
	if len(rows) <= limit {
		return rows
	}
	chosen := []Token{}
	seen := map[string]bool{}
	// Guarantee up to ten recent rows from each chain, then fill by score.
	for _, m := range chainModules {
		chainRows := []Token{}
		for _, t := range rows {
			if normalizeChain(t.Chain) == m.Key {
				chainRows = append(chainRows, t)
			}
		}
		sort.Slice(chainRows, func(i, j int) bool { return chainRows[i].UpdatedAt.After(chainRows[j].UpdatedAt) })
		if len(chainRows) > 10 {
			chainRows = chainRows[:10]
		}
		for _, t := range chainRows {
			k := tokenIdentity(t.Chain, t.Address)
			if !seen[k] {
				seen[k] = true
				chosen = append(chosen, t)
			}
		}
	}
	for _, t := range rows {
		if len(chosen) >= limit {
			break
		}
		k := tokenIdentity(t.Chain, t.Address)
		if !seen[k] {
			seen[k] = true
			chosen = append(chosen, t)
		}
	}
	sort.SliceStable(chosen, func(i, j int) bool {
		if chosen[i].Score == chosen[j].Score {
			return chosen[i].UpdatedAt.After(chosen[j].UpdatedAt)
		}
		return chosen[i].Score > chosen[j].Score
	})
	return chosen
}

func multiScanMarket(ctx context.Context, heldKeys []string) ([]Token, []string, scanStats, error) {
	started := time.Now()
	all, logs, latest, newFound, active, err := discoverAllModules(ctx)
	stats := scanStats{CandidatePool: len(all), NewFound: newFound, ActiveChains: active}
	parts := []string{}
	for _, m := range chainModules {
		if n := latest[m.Key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", m.Short, n))
			if m.Key == "base" {
				stats.LatestBlock = n
			}
		}
	}
	stats.ChainSummary = strings.Join(parts, " · ")
	if err != nil {
		stats.Duration = time.Since(started)
		return nil, logs, stats, err
	}
	batch := chooseMultiBatch(all, 15)
	byKey := map[string]multiCandidate{}
	groups := map[string][]string{}
	for _, c := range batch {
		k := tokenIdentity(c.Chain, c.TokenAddress)
		if _, ok := byKey[k]; !ok {
			byKey[k] = c
			groups[c.Chain] = append(groups[c.Chain], strings.ToLower(c.TokenAddress))
		}
	}
	for _, raw := range heldKeys {
		parts := strings.SplitN(raw, "|", 2)
		if len(parts) != 2 {
			continue
		}
		chain, address := normalizeChain(parts[0]), strings.ToLower(parts[1])
		if !validAddress(address) {
			continue
		}
		k := tokenIdentity(chain, address)
		if _, ok := byKey[k]; !ok {
			byKey[k] = multiCandidate{Chain: chain, TokenAddress: address, Factory: "模拟持仓跟踪", SeenAt: time.Now()}
			groups[chain] = append(groups[chain], address)
		}
	}
	stats.BatchAnalyzed = len(byKey)
	logs = append(logs, fmt.Sprintf("公平轮询：每链最多分析 15 个，本轮合计 %d 个", stats.BatchAnalyzed))
	universe := map[string]multiCandidate{}
	for _, c := range all {
		universe[tokenIdentity(c.Chain, c.TokenAddress)] = c
	}
	for k, c := range byKey {
		universe[k] = c
	}
	cache := loadMultiMarketCache()
	for k := range cache {
		if _, ok := universe[k]; !ok {
			delete(cache, k)
		}
	}
	for k, c := range universe {
		if old, ok := cache[k]; !ok {
			cache[k] = placeholderMulti(c)
		} else {
			old.Chain = c.Chain
			old.ChainID = chainIDFor(c.Chain)
			old.Source = chainLabel(c.Chain) + " · " + c.Factory
			old.PoolAddress = c.PoolAddress
			old.BlockNumber = c.BlockNumber
			cache[k] = old
		}
	}
	if len(byKey) == 0 {
		saveMultiMarketCache(cache)
		stats.Duration = time.Since(started)
		return topMultiDisplay(cache, 100), logs, stats, nil
	}

	best := map[string]dexPair{}
	security := map[string]map[string]any{}
	securityOK := map[string]bool{}
	for _, m := range chainModules {
		addresses := groups[m.Key]
		if len(addresses) == 0 {
			continue
		}
		for i := 0; i < len(addresses); i += 30 {
			j := i + 30
			if j > len(addresses) {
				j = len(addresses)
			}
			var pairs []dexPair
			u := "https://api.dexscreener.com/tokens/v1/" + m.DexSlug + "/" + strings.Join(addresses[i:j], ",")
			if e := getJSON(ctx, u, &pairs); e != nil {
				logs = append(logs, m.Short+" DEX补充失败："+shortErr(e))
				break
			}
			for _, p := range pairs {
				baseAddr := strings.ToLower(p.BaseToken.Address)
				quoteAddr := strings.ToLower(p.QuoteToken.Address)
				a := baseAddr
				if _, wanted := byKey[tokenIdentity(m.Key, a)]; !wanted {
					if _, quoteWanted := byKey[tokenIdentity(m.Key, quoteAddr)]; !quoteWanted {
						continue
					}
					// DEX Screener quotes priceUsd for baseToken.  When our candidate is
					// the quote token, keep the pool metrics but do not attach the other
					// token's price or project links to it.  This prevents false paper trades.
					a = quoteAddr
					p.BaseToken, p.QuoteToken = p.QuoteToken, p.BaseToken
					p.PriceUSD = ""
					p.PriceChange.H24 = 0
					p.Info.Websites = nil
					p.Info.Socials = nil
				}
				k := tokenIdentity(m.Key, a)
				if old, ok := best[k]; !ok || p.Liquidity.USD > old.Liquidity.USD {
					best[k] = p
				}
			}
		}
		sec, e := fetchSecurityForChain(ctx, m, addresses)
		if e != nil {
			logs = append(logs, m.Short+" GoPlus失败："+shortErr(e))
		} else {
			for a, v := range sec {
				k := tokenIdentity(m.Key, a)
				security[k] = v
				securityOK[k] = true
			}
			logs = append(logs, fmt.Sprintf("%s：市场命中 %d 个，安全返回 %d 个", m.Short, countBestForChain(best, m.Key), len(sec)))
		}
	}
	metaUsed := map[string]int{}
	for k, c := range byKey {
		m, okm := chainByKey(c.Chain)
		if !okm {
			continue
		}
		old := cache[k]
		p, marketOK := best[k]
		metadataChecked := old.MetadataCheckedAt
		if !marketOK {
			p.ChainID = m.DexSlug
			p.DEXID = "chain"
			p.PairAddress = c.PoolAddress
			p.URL = dexURLFor(c.Chain, c.PoolAddress)
			p.BaseToken.Address = c.TokenAddress
			p.BaseToken.Name = old.Name
			p.BaseToken.Symbol = old.Symbol
			if (p.BaseToken.Name == "" || strings.Contains(p.BaseToken.Name, "链上新代币") || p.BaseToken.Symbol == "NEW") && metaUsed[m.Key] < 4 && (metadataChecked.IsZero() || time.Since(metadataChecked) > 6*time.Hour) {
				name, symbol := tokenMetadataModule(ctx, m, c.TokenAddress)
				if name != "" {
					p.BaseToken.Name = name
				}
				if symbol != "" {
					p.BaseToken.Symbol = symbol
				}
				metadataChecked = time.Now()
				metaUsed[m.Key]++
			}
			if p.BaseToken.Symbol == "" || p.BaseToken.Symbol == "NEW" {
				a := c.TokenAddress
				if len(a) >= 8 {
					p.BaseToken.Symbol = strings.ToUpper(a[2:8])
				} else {
					p.BaseToken.Symbol = "NEW"
				}
			}
			if p.BaseToken.Name == "" || strings.Contains(p.BaseToken.Name, "链上新代币") {
				p.BaseToken.Name = m.Short + " 链上新代币"
			}
		}
		t := scorePair(p, security[k], securityOK[k])
		if !marketOK {
			if t.Score > 49 {
				t.Score = 49
			}
			t.Grade = "等待市场数据"
			filtered := t.Evidence[:0]
			for _, e := range t.Evidence {
				if strings.Contains(e, "流动性低于") || strings.Contains(e, "24H 成交量很低") {
					continue
				}
				filtered = append(filtered, e)
			}
			t.Evidence = filtered
		}
		t.Chain = m.Key
		t.ChainID = m.ChainID
		t.Source = m.Short + " · " + c.Factory
		t.PoolAddress = c.PoolAddress
		t.BlockNumber = c.BlockNumber
		t.UpdatedAt = time.Now()
		t.MetadataCheckedAt = metadataChecked
		t.DEXURL = p.URL
		t.Evidence = append([]string{fmt.Sprintf("%s 链直接发现：%s 新池，区块 %d", m.Short, c.Factory, c.BlockNumber)}, t.Evidence...)
		if !marketOK {
			t.Evidence = append(t.Evidence, "DEX Screener 尚未索引该池，价格与流动性暂缺")
		}
		cache[k] = t
	}
	saveMultiMarketCache(cache)
	stats.Duration = time.Since(started)
	return topMultiDisplay(cache, 100), logs, stats, nil
}

func countBestForChain(best map[string]dexPair, chain string) int {
	n := 0
	prefix := normalizeChain(chain) + "|"
	for k := range best {
		if strings.HasPrefix(k, prefix) {
			n++
		}
	}
	return n
}

func diagnoseMultiChain() string {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var b strings.Builder
	b.WriteString("连接诊断结果（V2.5 多链版）\n\n网络引擎：Windows WinHTTP 自动代理优先，Go 直连/环境代理兜底\n系统代理：" + windowsProxySummary() + "\n\n")
	okChains := 0
	for _, m := range chainModules {
		st := time.Now()
		var block string
		ep, engine, e := rpcCallModule(ctx, m, "eth_blockNumber", []any{}, &block)
		if e != nil {
			b.WriteString("✕ " + m.Short + " RPC：" + fullErr(e) + "\n\n")
			continue
		}
		n, _ := parseHexUint(block)
		okChains++
		b.WriteString(fmt.Sprintf("✓ %s RPC：区块 %d，%s，%d ms\n   %s\n\n", m.Short, n, engine, time.Since(st).Milliseconds(), ep))
	}
	tests := []struct{ name, url string }{{"DEX Screener", "https://api.dexscreener.com/token-profiles/latest/v1"}, {"GoPlus BSC", "https://api.gopluslabs.io/api/v1/token_security/56?contract_addresses=0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c"}, {"GoPlus Arbitrum", "https://api.gopluslabs.io/api/v1/token_security/42161?contract_addresses=0x82af49447d8a07e3bd95bd0d56f35241523fbab1"}}
	for _, t := range tests {
		st := time.Now()
		res, e := fetchURL(ctx, t.url)
		if e != nil {
			b.WriteString("✕ " + t.name + "：" + fullErr(e) + "\n\n")
			continue
		}
		if res.Status >= 200 && res.Status < 300 {
			b.WriteString(fmt.Sprintf("✓ %s：HTTP %d，%s，%d ms\n\n", t.name, res.Status, res.Engine, time.Since(st).Milliseconds()))
		} else {
			b.WriteString(fmt.Sprintf("△ %s：HTTP %d\n\n", t.name, res.Status))
		}
	}
	b.WriteString(fmt.Sprintf("结论：%d/%d 条链可连接。任意一条链正常时程序都可继续监控；失败链会单独跳过，不会拖死整个扫描。", okChains, len(chainModules)))
	return b.String()
}
