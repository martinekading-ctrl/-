//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	Confirmations   uint64
}

const (
	pancakeV2BSC       = "0xca143ce32fe78f1f7019d7d551a6402fc5350c73"
	pancakeV2EVM       = "0x02a84c1b3bbd7401a5f7fa98a384ebc70bb5749e"
	pancakeV3EVM       = "0x0bfbcf9fa4f9c56b0f40a671ad40e0805a091865"
	uniswapV2Ethereum  = "0x5c69bee701ef814a2b6a3edd4b1652cb9cc5aa6f"
	uniswapV3Canonical = "0x1f98431c8ad98523631ae4a59f267346ea31f984"
	quickSwapV2Polygon = "0x5757371414417b8c6caad45baef941abc7d3ab32"
)

var chainModules = []chainModule{
	{
		Key: "ethereum", Name: "Ethereum", Short: "ETH", ChainID: "1", DexSlug: "ethereum", EnvRPC: "ETHEREUM_RPC_URL",
		RPCs: []string{"https://ethereum-rpc.publicnode.com", "https://1rpc.io/eth"},
		Factories: []factorySpec{
			{Name: "Uniswap V2", Address: uniswapV2Ethereum, Topic: topicUniswapV2PairCreated, Kind: "v2"},
			{Name: "Uniswap V3", Address: uniswapV3Canonical, Topic: topicUniswapV3PoolCreated, Kind: "v3"},
		},
		KnownAssets: map[string]bool{
			"0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2": true,
			"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48": true,
			"0xdac17f958d2ee523a2206206994597c13d831ec7": true,
			"0x6b175474e89094c44da98b954eedeac495271d0f": true,
			"0x2260fac5e5542a773aa44fbcfedf7c193bc2c599": true,
		}, InitialLookback: 120, ChunkSize: 40, Confirmations: 6,
	},
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
		}, InitialLookback: 1800, ChunkSize: 500, Confirmations: 4,
	},
	{
		Key: "bsc", Name: "BNB Smart Chain", Short: "BSC", ChainID: "56", DexSlug: "bsc", EnvRPC: "BSC_RPC_URL",
		// 1RPC accepts no-key BSC logs in small ranges. Keep its 10-block limit
		// below the module chunk size so public endpoints remain usable on a
		// desktop proxy; users can still override with BSC_RPC_URL.
		RPCs: []string{"https://1rpc.io/bnb", "https://bsc-rpc.publicnode.com", "https://bnb.rpc.subquery.network/public"},
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
		}, InitialLookback: 60, ChunkSize: 10, Confirmations: 6,
	},
	{
		Key: "optimism", Name: "Optimism", Short: "OP", ChainID: "10", DexSlug: "optimism", EnvRPC: "OPTIMISM_RPC_URL",
		RPCs: []string{"https://optimism-rpc.publicnode.com", "https://mainnet.optimism.io"},
		Factories: []factorySpec{
			{Name: "Uniswap V3", Address: uniswapV3Canonical, Topic: topicUniswapV3PoolCreated, Kind: "v3"},
		},
		KnownAssets: map[string]bool{
			"0x4200000000000000000000000000000000000006": true,
			"0x0b2c639c533813f4aa9d7837caf62653d097ff85": true,
			"0x94b008aa00579c1307b0ef2c499ad98a8ce58e58": true,
			"0xda10009cbd5d07dd0cecc66161fc93d7c9000da1": true,
			"0x4200000000000000000000000000000000000042": true,
		}, InitialLookback: 600, ChunkSize: 100, Confirmations: 8,
	},
	{
		Key: "polygon", Name: "Polygon PoS", Short: "POL", ChainID: "137", DexSlug: "polygon", EnvRPC: "POLYGON_RPC_URL",
		RPCs: []string{"https://polygon-bor-rpc.publicnode.com", "https://polygon.drpc.org"},
		Factories: []factorySpec{
			{Name: "QuickSwap V2", Address: quickSwapV2Polygon, Topic: topicUniswapV2PairCreated, Kind: "v2"},
			{Name: "Uniswap V3", Address: uniswapV3Canonical, Topic: topicUniswapV3PoolCreated, Kind: "v3"},
		},
		KnownAssets: map[string]bool{
			"0x0d500b1d8e8ef31e21c99d1db9a6444d3adf1270": true,
			"0x3c499c542cef5e3811e1192ce70d8cc03d5c3359": true,
			"0x2791bca1f2de4661ed88a30c99a7a9449aa84174": true,
			"0x7ceb23fd6bc0add59e62ac25578270cff1b9f619": true,
			"0x8f3cf7ad23cd3cadbd9735aff958023239c6a063": true,
		}, InitialLookback: 300, ChunkSize: 100, Confirmations: 8,
	},
	{
		Key: "arbitrum", Name: "Arbitrum One", Short: "ARB", ChainID: "42161", DexSlug: "arbitrum", EnvRPC: "ARBITRUM_RPC_URL",
		RPCs: []string{"https://arb1.arbitrum.io/rpc", "https://arbitrum-one-rpc.publicnode.com"},
		Factories: []factorySpec{
			{Name: "Uniswap V3", Address: uniswapV3Canonical, Topic: topicUniswapV3PoolCreated, Kind: "v3"},
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
		}, InitialLookback: 2400, ChunkSize: 400, Confirmations: 6,
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
		}
		// Unknown/unknown pools are intentionally not candidates. They have no
		// trustworthy price leg and are a common way for an old token to create a
		// fresh auxiliary pool that looks like a launch event.
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
	confirmed := confirmedBlock(latest, m.Confirmations)
	if confirmed == 0 {
		return moduleDiscoverResult{Module: m, LastBlock: last, Err: fmt.Errorf("%s RPC 区块确认不足", m.Short)}
	}
	logs := []string{fmt.Sprintf("%s RPC：区块 %d，确认扫描至 %d，%s，%s", m.Short, latest, confirmed, engine, ep)}
	from := last + 1
	if last == 0 || (from <= confirmed && confirmed-from > m.InitialLookback*2) {
		if confirmed > m.InitialLookback {
			from = confirmed - m.InitialLookback
		} else {
			from = 0
		}
	}
	if from > confirmed {
		return moduleDiscoverResult{Module: m, Logs: logs, Latest: confirmed, LastBlock: last}
	}
	found, more, completed, complete := scanModuleRange(ctx, m, from, confirmed)
	logs = append(logs, more...)
	newLast := last
	if complete {
		newLast = confirmed
	} else if completed >= from {
		newLast = completed
	}
	logs = append(logs, fmt.Sprintf("%s：新发现 %d 条，游标 %d", m.Short, len(found), newLast))
	if !complete {
		return moduleDiscoverResult{
			Module: m, Candidates: found, Logs: logs, Latest: confirmed,
			NewFound: len(found), LastBlock: newLast,
			Err: fmt.Errorf("%s 新池日志扫描未完成", m.Short),
		}
	}
	return moduleDiscoverResult{Module: m, Candidates: found, Logs: logs, Latest: confirmed, NewFound: len(found), LastBlock: newLast}
}

// confirmedBlock deliberately leaves a small reorg and node-indexing margin.
// New-pool events are picked up on the next pass instead of being skipped when
// a public RPC reports a head block before its log index is ready.
func confirmedBlock(latest, confirmations uint64) uint64 {
	if latest <= confirmations {
		return 0
	}
	return latest - confirmations
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

type securityBackoffState struct {
	Failures int
	Until    time.Time
	LastErr  string
}

var securityBackoffMu sync.Mutex
var securityBackoffs = map[string]securityBackoffState{}

// DEX Screener is a separate public dependency. Keeping an independent
// backoff prevents a transient market-data outage from amplifying into a burst
// of requests while security data remains available for other chains.
var marketBackoffMu sync.Mutex
var marketBackoffs = map[string]securityBackoffState{}

func securityBackoffDuration(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	// Public security endpoints can rate-limit a busy desktop scanner. Back off
	// per chain rather than retrying every scan; cap at 30 minutes so recovery is
	// eventually rechecked without creating a request storm.
	d := time.Minute << minInt(failures-1, 5)
	if d > 30*time.Minute {
		return 30 * time.Minute
	}
	return d
}

func securityRequestAllowed(chain string, now time.Time) error {
	securityBackoffMu.Lock()
	defer securityBackoffMu.Unlock()
	b := securityBackoffs[normalizeChain(chain)]
	if now.Before(b.Until) {
		return fmt.Errorf("GoPlus 安全接口退避中，%.0f 分钟后重试：%s", math.Ceil(b.Until.Sub(now).Minutes()), b.LastErr)
	}
	return nil
}

func recordSecurityFailure(chain string, now time.Time, err error) {
	securityBackoffMu.Lock()
	defer securityBackoffMu.Unlock()
	key := normalizeChain(chain)
	b := securityBackoffs[key]
	b.Failures++
	b.LastErr = shortErr(err)
	b.Until = now.Add(securityBackoffDuration(b.Failures))
	securityBackoffs[key] = b
}

func clearSecurityBackoff(chain string) {
	securityBackoffMu.Lock()
	delete(securityBackoffs, normalizeChain(chain))
	securityBackoffMu.Unlock()
}

func marketRequestAllowed(chain string, now time.Time) error {
	marketBackoffMu.Lock()
	defer marketBackoffMu.Unlock()
	b := marketBackoffs[normalizeChain(chain)]
	if now.Before(b.Until) {
		return fmt.Errorf("DEX 市场接口退避中，%.0f 分钟后重试：%s", math.Ceil(b.Until.Sub(now).Minutes()), b.LastErr)
	}
	return nil
}

func recordMarketFailure(chain string, now time.Time, err error) {
	marketBackoffMu.Lock()
	defer marketBackoffMu.Unlock()
	key := normalizeChain(chain)
	b := marketBackoffs[key]
	b.Failures++
	b.LastErr = shortErr(err)
	b.Until = now.Add(securityBackoffDuration(b.Failures))
	marketBackoffs[key] = b
}

func clearMarketBackoff(chain string) {
	marketBackoffMu.Lock()
	delete(marketBackoffs, normalizeChain(chain))
	marketBackoffMu.Unlock()
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
	if e := securityRequestAllowed(m.Key, now); e != nil {
		return out, e
	}
	var resp struct {
		Code    int                       `json:"code"`
		Message string                    `json:"message"`
		Result  map[string]map[string]any `json:"result"`
	}
	u := "https://api.gopluslabs.io/api/v1/token_security/" + m.ChainID + "?contract_addresses=" + url.QueryEscape(strings.Join(missing, ","))
	if e := getJSON(ctx, u, &resp); e != nil {
		recordSecurityFailure(m.Key, now, e)
		return out, e
	}
	if resp.Code != 1 || resp.Result == nil {
		e := fmt.Errorf("GoPlus %s 业务错误：code=%d message=%s", m.Short, resp.Code, resp.Message)
		recordSecurityFailure(m.Key, now, e)
		return out, e
	}
	clearSecurityBackoff(m.Key)
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
	if limit <= 0 {
		return nil
	}
	strict := make([]Token, 0, len(cache))
	review := make([]Token, 0, len(cache))
	now := time.Now()
	for _, t := range cache {
		if t.PotentialEligible {
			strict = append(strict, t)
			continue
		}
		// A strict signal must never be fabricated just to keep the screen busy.
		// Still, showing a compact, recent review queue lets the operator inspect
		// why a fresh candidate was held or rejected.  These rows remain read-only:
		// PotentialEligible is carried into SimQuote and is required by every buy
		// path.
		if !t.UpdatedAt.IsZero() && now.Sub(t.UpdatedAt) <= 6*time.Hour && t.PotentialStage != "" {
			review = append(review, t)
		}
	}
	sort.SliceStable(strict, func(i, j int) bool {
		if strict[i].Score == strict[j].Score {
			return strict[i].UpdatedAt.After(strict[j].UpdatedAt)
		}
		return strict[i].Score > strict[j].Score
	})
	sort.SliceStable(review, func(i, j int) bool {
		// Candidates waiting for an exact-market index are useful to revisit first;
		// all remaining rows are ordered by freshness, then by their research score.
		iWait := strings.Contains(review[i].PotentialStage, "等待精确池索引")
		jWait := strings.Contains(review[j].PotentialStage, "等待精确池索引")
		if iWait != jWait {
			return iWait
		}
		if !review[i].UpdatedAt.Equal(review[j].UpdatedAt) {
			return review[i].UpdatedAt.After(review[j].UpdatedAt)
		}
		return review[i].Score > review[j].Score
	})

	// Keep the actionable layer first.  The read-only layer is deliberately
	// capped so a burst of rejected pools cannot bury a real strict signal.
	chosen := append([]Token{}, strict...)
	if len(chosen) > limit {
		return chosen[:limit]
	}
	reviewCap := 30
	if len(strict) > 0 {
		reviewCap = 12
	}
	remaining := limit - len(chosen)
	if reviewCap < remaining {
		remaining = reviewCap
	}
	if remaining > len(review) {
		remaining = len(review)
	}
	if remaining > 0 {
		chosen = append(chosen, review[:remaining]...)
	}
	return chosen
}

const (
	strictMarketFirstWindow = time.Hour
	strictMinLiquidity      = 10_000.0
	strictMinH1Buys         = 4
	strictMinHolders        = 20
	strictMaxAdminPct       = 0.20
	// Automatic paper entries are intentionally stricter than the research
	// score. These limits apply only to the "严格观察" funnel, never to the
	// read-only review list.
	strictMaxTopHolderPct = 0.15
	strictMaxTopTenPct    = 0.60
	strictMinLPLockPct    = 0.80
)

// exactPoolAssessment keeps the scanner honest about which DEX pool it is
// scoring.  A token endpoint can return every historical pool for a token;
// choosing the largest one is precisely what made an old token look new.
type exactPoolAssessment struct {
	Pair                dexPair
	Exact               bool
	HasOlderIndexedPool bool
	Reason              string
}

func pairContainsToken(p dexPair, token string) bool {
	token = strings.ToLower(token)
	return strings.EqualFold(p.BaseToken.Address, token) || strings.EqualFold(p.QuoteToken.Address, token)
}

func assessExactPool(c multiCandidate, pairs []dexPair) exactPoolAssessment {
	q := exactPoolAssessment{}
	for _, p := range pairs {
		if !strings.EqualFold(p.PairAddress, c.PoolAddress) {
			continue
		}
		if !pairContainsToken(p, c.TokenAddress) {
			continue
		}
		if !strings.EqualFold(p.BaseToken.Address, c.TokenAddress) {
			q.Reason = "精确新池未提供该代币的美元报价"
			continue
		}
		q.Pair, q.Exact = p, true
		break
	}
	if !q.Exact {
		if q.Reason == "" {
			q.Reason = "DEX 尚未索引精确新池"
		}
		return q
	}
	if q.Pair.PairCreatedAt <= 0 {
		q.Exact = false
		q.Reason = "精确新池创建时间尚未索引"
		return q
	}
	// This is a market-age check, not a claim about contract deployment time.
	// It catches the important case where an old token opens a fresh pool.
	for _, p := range pairs {
		if !pairContainsToken(p, c.TokenAddress) || strings.EqualFold(p.PairAddress, c.PoolAddress) || p.PairCreatedAt <= 0 {
			continue
		}
		if p.PairCreatedAt < q.Pair.PairCreatedAt-60_000 {
			q.HasOlderIndexedPool = true
			break
		}
	}
	return q
}

func strictMarketFirstQualification(q exactPoolAssessment, t Token, sec map[string]any, securityOK bool) (bool, string) {
	if !q.Exact {
		return false, q.Reason
	}
	if q.HasOlderIndexedPool {
		return false, "发现更早的 DEX 市场记录，排除老币新开辅助池"
	}
	age := time.Since(time.UnixMilli(q.Pair.PairCreatedAt))
	if age < 0 || age > strictMarketFirstWindow {
		return false, "精确池不在 60 分钟市场首发窗口内"
	}
	if t.Price <= 0 || t.Liquidity < strictMinLiquidity {
		return false, "精确池价格或流动性未达到 10,000 美元"
	}
	if q.Pair.Txns.H1.Buys < strictMinH1Buys {
		return false, "近 1 小时真实买入笔数不足 4 笔"
	}
	if q.Pair.Txns.H1.Sells > q.Pair.Txns.H1.Buys {
		return false, "近 1 小时卖出笔数高于买入笔数"
	}
	if !securityOK || t.Security != "已验证" {
		return false, "安全接口未完整验证"
	}
	if !isOne(sec, "is_open_source") {
		return false, "合约未确认开源"
	}
	for _, key := range []string{"is_honeypot", "cannot_sell_all", "is_blacklisted", "owner_change_balance", "selfdestruct", "is_proxy", "is_mintable", "hidden_owner", "transfer_pausable", "slippage_modifiable", "personal_slippage_modifiable", "trading_cooldown", "anti_whale_modifiable", "external_call"} {
		if val(sec, key) == "" {
			return false, "安全接口缺少自动开仓必需字段：" + key
		}
		if isOne(sec, key) {
			return false, "安全硬门槛未通过：" + key
		}
	}
	if !t.TaxKnown || t.BuyTaxPct > 5 || t.SellTaxPct > 5 {
		return false, "买卖税未确认或高于 5%"
	}
	if ownerPct := math.Max(fnum(val(sec, "owner_percent")), fnum(val(sec, "creator_percent"))); ownerPct > strictMaxAdminPct {
		return false, "创建者或所有者持仓超过 20%"
	}
	if holders := int(fnum(val(sec, "holder_count"))); holders < strictMinHolders {
		return false, "持币地址少于 20 个"
	}
	maxHolder, topTen, listed := holderConcentration(sec)
	if listed == 0 {
		return false, "安全接口未返回可核验的前十持仓分布"
	}
	if maxHolder > strictMaxTopHolderPct {
		return false, fmt.Sprintf("最大未锁定持仓 %.1f%% 超过严格上限 %.1f%%", maxHolder*100, strictMaxTopHolderPct*100)
	}
	if topTen > strictMaxTopTenPct {
		return false, fmt.Sprintf("前十未锁定持仓合计 %.1f%% 超过严格上限 %.1f%%", topTen*100, strictMaxTopTenPct*100)
	}
	if !t.LPLockKnown {
		return false, "未取得可核验的 LP 锁定分布"
	}
	if t.LPLockPct < strictMinLPLockPct {
		return false, fmt.Sprintf("LP 锁定 %.1f%% 低于严格下限 %.1f%%", t.LPLockPct*100, strictMinLPLockPct*100)
	}
	return true, "通过严格市场首发资格：精确新池、无更早 DEX 记录、安全完整、LP 锁定与持仓分布均通过"
}

func topQualificationReasons(reasons map[string]int, limit int) string {
	type reasonCount struct {
		reason string
		count  int
	}
	rows := make([]reasonCount, 0, len(reasons))
	for reason, count := range reasons {
		if count > 0 {
			rows = append(rows, reasonCount{reason: reason, count: count})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count == rows[j].count {
			return rows[i].reason < rows[j].reason
		}
		return rows[i].count > rows[j].count
	})
	if limit <= 0 || limit > len(rows) {
		limit = len(rows)
	}
	parts := make([]string, 0, limit)
	for _, row := range rows[:limit] {
		parts = append(parts, fmt.Sprintf("%s %d", row.reason, row.count))
	}
	return strings.Join(parts, "；")
}

func appendFunnelAlert(rows []string, message string) []string {
	if message == "" {
		return rows
	}
	for _, row := range rows {
		if row == message {
			return rows
		}
	}
	if len(rows) >= 8 {
		return rows
	}
	return append(rows, message)
}

func riskSnapshotAlerts(old, current Token) []string {
	if old.RiskCheckedAt.IsZero() {
		return nil
	}
	prefix := fmt.Sprintf("[%s] %s", chainLabel(current.Chain), current.Symbol)
	alerts := []string{}
	if old.Liquidity > 0 && current.Liquidity > 0 && current.Liquidity < old.Liquidity*0.70 {
		alerts = append(alerts, fmt.Sprintf("%s 流动性下降 %.0f%%", prefix, (1-current.Liquidity/old.Liquidity)*100))
	}
	if old.LPLockKnown && current.LPLockKnown && current.LPLockPct+0.10 < old.LPLockPct {
		alerts = append(alerts, fmt.Sprintf("%s LP 锁定比例 %.0f%% → %.0f%%", prefix, old.LPLockPct*100, current.LPLockPct*100))
	}
	if old.CreatorPercent > 0 && current.CreatorPercent > old.CreatorPercent+0.05 {
		alerts = append(alerts, fmt.Sprintf("%s 创建者持仓 %.1f%% → %.1f%%", prefix, old.CreatorPercent*100, current.CreatorPercent*100))
	}
	if old.TopHolderPercent > 0 && current.TopHolderPercent > old.TopHolderPercent+0.10 {
		alerts = append(alerts, fmt.Sprintf("%s 最大持仓 %.1f%% → %.1f%%", prefix, old.TopHolderPercent*100, current.TopHolderPercent*100))
	}
	return alerts
}

func strictRiskWarnings(t Token) []string {
	prefix := fmt.Sprintf("[%s] %s", chainLabel(t.Chain), t.Symbol)
	alerts := []string{}
	if !t.LPLockKnown {
		alerts = append(alerts, prefix+" LP 锁定状态未验证")
	} else if t.LPLockPct < 0.80 {
		alerts = append(alerts, fmt.Sprintf("%s LP 锁定仅 %.0f%%", prefix, t.LPLockPct*100))
	}
	if t.CreatorPercent > 0.10 {
		alerts = append(alerts, fmt.Sprintf("%s 创建者持仓 %.1f%%", prefix, t.CreatorPercent*100))
	}
	if t.TopHolderPercent > 0.30 {
		alerts = append(alerts, fmt.Sprintf("%s 最大持仓 %.1f%%", prefix, t.TopHolderPercent*100))
	}
	return alerts
}

// resolveTrackedCandidate rebuilds a durable scanner reference for an open
// paper position or watched token. The entry pool wins over every fallback:
// using a newer auxiliary pool for the same token would make marks and exits
// refer to a different market than the simulated entry.
func resolveTrackedCandidate(raw multiCandidate, cached Token, cacheOK bool, discovered multiCandidate, discoveredOK bool) multiCandidate {
	c := multiCandidate{
		Chain:        normalizeChain(raw.Chain),
		TokenAddress: strings.ToLower(raw.TokenAddress),
		PoolAddress:  strings.ToLower(raw.PoolAddress),
		Factory:      raw.Factory,
		BlockNumber:  raw.BlockNumber,
		SeenAt:       raw.SeenAt,
	}
	if c.PoolAddress == "" && cacheOK {
		c.PoolAddress = strings.ToLower(cached.PoolAddress)
		if c.BlockNumber == 0 {
			c.BlockNumber = cached.BlockNumber
		}
	}
	if discoveredOK {
		if c.PoolAddress == "" {
			c.PoolAddress = discovered.PoolAddress
		}
		if c.BlockNumber == 0 {
			c.BlockNumber = discovered.BlockNumber
		}
		if c.Factory == "" {
			c.Factory = discovered.Factory
		}
		if c.SeenAt.IsZero() {
			c.SeenAt = discovered.SeenAt
		}
	}
	if c.Factory == "" {
		c.Factory = "持仓/监控跟踪"
	}
	if c.SeenAt.IsZero() {
		c.SeenAt = time.Now()
	}
	return c
}

func multiScanMarket(ctx context.Context, heldRefs []multiCandidate) ([]Token, []string, scanStats, error) {
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
	discoveredByKey := map[string]multiCandidate{}
	for _, c := range all {
		discoveredByKey[tokenIdentity(c.Chain, c.TokenAddress)] = c
	}
	cache := loadMultiMarketCache()
	groups := map[string][]string{}
	for _, c := range batch {
		k := tokenIdentity(c.Chain, c.TokenAddress)
		if _, ok := byKey[k]; !ok {
			byKey[k] = c
			groups[c.Chain] = append(groups[c.Chain], strings.ToLower(c.TokenAddress))
		}
	}
	for _, raw := range heldRefs {
		chain, address := normalizeChain(raw.Chain), strings.ToLower(raw.TokenAddress)
		if !validAddress(address) {
			continue
		}
		k := tokenIdentity(chain, address)
		if current, ok := byKey[k]; ok {
			// A held position is tied to its entry pool. Prefer that durable pool
			// reference over a newly-discovered auxiliary pool for the same token.
			if raw.PoolAddress != "" {
				current.PoolAddress = strings.ToLower(raw.PoolAddress)
			}
			byKey[k] = current
			continue
		}
		cached, cacheOK := cache[k]
		discovered, discoveredOK := discoveredByKey[k]
		c := resolveTrackedCandidate(raw, cached, cacheOK, discovered, discoveredOK)
		byKey[k] = c
		groups[chain] = append(groups[chain], address)
	}
	stats.BatchAnalyzed = len(byKey)
	logs = append(logs, fmt.Sprintf("公平轮询：每链最多分析 15 个，本轮合计 %d 个", stats.BatchAnalyzed))
	universe := discoveredByKey
	for k, c := range byKey {
		if discovered, ok := universe[k]; ok {
			if c.PoolAddress == "" {
				c.PoolAddress = discovered.PoolAddress
			}
			if c.BlockNumber == 0 {
				c.BlockNumber = discovered.BlockNumber
			}
			if c.Factory == "" {
				c.Factory = discovered.Factory
			}
		}
		universe[k] = c
		byKey[k] = c
	}
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
			if c.Factory != "" && (c.Factory != "模拟持仓跟踪" || old.Source == "") {
				old.Source = chainLabel(c.Chain) + " · " + c.Factory
			}
			// A transient tracking reference must never erase the exact pool that
			// an open paper position was entered through.
			if c.PoolAddress != "" {
				old.PoolAddress = c.PoolAddress
				old.DEXURL = dexURLFor(c.Chain, c.PoolAddress)
			}
			if c.BlockNumber != 0 {
				old.BlockNumber = c.BlockNumber
			}
			cache[k] = old
		}
	}
	if len(byKey) == 0 {
		saveMultiMarketCache(cache)
		stats.Duration = time.Since(started)
		return topMultiDisplay(cache, 100), logs, stats, nil
	}

	marketPairs := map[string][]dexPair{}
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
			if e := marketRequestAllowed(m.Key, time.Now()); e != nil {
				logs = append(logs, m.Short+" DEX市场数据暂不可用："+shortErr(e))
				break
			}
			if e := getJSON(ctx, u, &pairs); e != nil {
				recordMarketFailure(m.Key, time.Now(), e)
				logs = append(logs, m.Short+" DEX补充失败："+shortErr(e))
				break
			}
			clearMarketBackoff(m.Key)
			for _, p := range pairs {
				baseAddr := strings.ToLower(p.BaseToken.Address)
				quoteAddr := strings.ToLower(p.QuoteToken.Address)
				for _, a := range []string{baseAddr, quoteAddr} {
					k := tokenIdentity(m.Key, a)
					if _, wanted := byKey[k]; wanted {
						marketPairs[k] = append(marketPairs[k], p)
					}
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
			logs = append(logs, fmt.Sprintf("%s：市场候选 %d 个，安全返回 %d 个", m.Short, countMarketPairsForChain(marketPairs, m.Key), len(sec)))
		}
	}
	metaUsed := map[string]int{}
	rejectionReasons := map[string]int{}
	for k, c := range byKey {
		m, okm := chainByKey(c.Chain)
		if !okm {
			continue
		}
		old := cache[k]
		assessment := assessExactPool(c, marketPairs[k])
		p, marketOK := assessment.Pair, assessment.Exact
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
		if !marketOK {
			stats.Awaiting++
			t.PotentialEligible = false
			t.PotentialStage = "等待精确池索引"
			t.Evidence = append(t.Evidence, assessment.Reason+"；不会进入雷达或模拟交易")
		} else {
			ok, reason := strictMarketFirstQualification(assessment, t, security[k], securityOK[k])
			if ok {
				stats.Qualified++
				t.PotentialEligible = true
				t.PotentialStage = "严格观察"
				t.Grade = "严格观察"
				t.Evidence = append([]string{reason, "说明：这是市场首发代理证据，不等同于合约部署时间证明。"}, t.Evidence...)
			} else {
				stats.Rejected++
				rejectionReasons[reason]++
				t.PotentialEligible = false
				t.PotentialStage = "淘汰：" + reason
				t.Evidence = append([]string{"未通过严格市场首发资格：" + reason}, t.Evidence...)
			}
		}
		if t.PotentialEligible && !old.PotentialEligible {
			stats.StageAlerts = appendFunnelAlert(stats.StageAlerts, fmt.Sprintf("[%s] %s 进入严格观察", m.Short, t.Symbol))
			for _, alert := range strictRiskWarnings(t) {
				stats.RiskAlerts = appendFunnelAlert(stats.RiskAlerts, alert)
			}
		}
		if old.PotentialEligible && !t.PotentialEligible {
			stats.StageAlerts = appendFunnelAlert(stats.StageAlerts, fmt.Sprintf("[%s] %s 退出严格观察：%s", m.Short, t.Symbol, t.PotentialStage))
		}
		if old.PotentialEligible || t.PotentialEligible {
			for _, alert := range riskSnapshotAlerts(old, t) {
				stats.RiskAlerts = appendFunnelAlert(stats.RiskAlerts, alert)
			}
		}
		if !t.PotentialEligible && marketOK && t.Price > 0 && t.Security != "严重风险" && !strings.Contains(t.PotentialStage, "安全硬门槛") {
			stats.ReviewQuotes = append(stats.ReviewQuotes, FunnelReviewQuote{Chain: t.Chain, Address: t.Address, Symbol: t.Symbol, Price: t.Price, Liquidity: t.Liquidity, Stage: t.PotentialStage, Security: t.Security, Time: t.UpdatedAt})
		}
		cache[k] = t
	}
	stats.RejectSummary = topQualificationReasons(rejectionReasons, 3)
	logs = append(logs, fmt.Sprintf("严格市场首发：通过 %d，等待精确池索引 %d，淘汰 %d；主要原因：%s", stats.Qualified, stats.Awaiting, stats.Rejected, stats.RejectSummary))
	saveMultiMarketCache(cache)
	stats.Duration = time.Since(started)
	return topMultiDisplay(cache, 100), logs, stats, nil
}

func countMarketPairsForChain(pairs map[string][]dexPair, chain string) int {
	n := 0
	prefix := normalizeChain(chain) + "|"
	for k := range pairs {
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
