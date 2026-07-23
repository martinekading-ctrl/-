# Multi-Chain Token Radar V2.12

V2.12 is a Windows x64 research application for Base, BSC, and Arbitrum. Its
current workflow is:

1. discover recently created DEX pools;
2. enrich candidates with public market and contract-risk data;
3. observe price and liquidity before entry;
4. execute local paper trades with estimated costs;
5. exit automatically under deterministic risk and profit rules;
6. validate the strategy from complete automated positions.

The application does not connect to a wallet, store private keys, sign orders,
or submit real transactions. A profitable paper result is not a promise of
future or live-trading profit.

## V2.12 Chinese token research entry point

- Every candidate now has a **中文资料** link that opens the exact token
  contract on OKLink's Simplified Chinese interface for Base, BNB Chain, and
  Arbitrum One.
- The original exact-pool DexScreener link remains available as **原始走势**.
  It is kept because a just-created pool may not yet be indexed by a Chinese
  market site.
- The compact detail panel prioritizes **中文资料**; the expanded detail view
  provides Chinese token information, the original market chart, and the
  chain explorer together.
- Candidate CSV exports now contain a Chinese-token-page column in addition
  to the original chart and explorer URLs.

## V2.11 exploration samples and rejection statistics

- Existing simulation accounts migrate to the default **探索** profile: a
  capped, 1-USDC paper-only research lane with a 15-point score floor, 5,000
  USDC liquidity floor, and a roughly 30-second observation window.
- Serious risk, zero-quality candidates, stale prices, and known taxes above
  20% remain hard stops even in exploration.
- Exploration positions use tighter exits and a 30-minute maximum hold so they
  produce reviewable paper outcomes instead of accumulating indefinitely.
- Near-miss candidates with a score of at least 10 can become **shadow
  samples**. Shadow samples never reserve simulated cash, never appear as
  trades, and never count as profitability validation.
- The strategy panel now shows the latest evaluated-candidate count, eligible
  count, opened exploration samples, started shadow samples, and the top three
  rejection reasons.
- Strict validation excludes exploration positions and shadow samples. Only
  non-exploratory automated positions can satisfy the paper-validation gate.

## V2.10 full-page scrolling and readable details

- The radar and paper-trading pages are taller than the window and support
  mouse-wheel vertical scrolling. The bottom status bar remains visible.
- Scrolling over the candidate table moves through tokens; scrolling over the
  selected-token panel or unused space moves the entire page.
- The compact selected-token panel reserves a separate footer for the contract
  address, so evidence, pool data, and contract text cannot overlap.
- A page scrollbar on the right shows the current vertical position.

- Every candidate row has visible **原始走势** and **中文资料** links.
- The selected-token panel prioritizes **中文资料** and retains the original
  exact-pool chart as **原始走势**.
- **展开** opens a large details view with the complete chart URL, explorer
  URL, contract address, pool address, metrics, and all evidence.
- The candidate list scrolls continuously by three rows per mouse-wheel step
  and displays a proportional scrollbar. Page buttons remain available.
- The expanded details view also supports mouse-wheel scrolling.
- Candidate CSV exports now include chart and explorer URLs for every token.

## Paper strategy

Paper automation is enabled by default for new accounts and is enabled once
when an older local simulation account is migrated. It uses virtual USDC only.

Entry protection now includes:

- conservative, standard, and test profiles;
- fresh-quote, pool-age, score, security, tax, and buy/sell-ratio gates;
- a real observation period instead of entering on one quote;
- rejection of unstable liquidity and abnormal one-step price jumps;
- pullback-and-recovery confirmation for conservative and standard profiles;
- one open position per chain;
- liquidity-aware position sizing, capped at 5 USDC;
- smaller sizing after consecutive losses.

Exit protection includes estimated DEX fees, gas, taxes, and slippage; an 8%
net stop; partial profit-taking at 12%; final profit-taking at 20%; an 8%
trailing exit; a six-hour maximum hold; a 25% liquidity-drop exit; a security
or score deterioration exit; and cost protection after the first take-profit.

Three consecutive losing automated positions pause new entries for three
hours. The existing daily loss limit remains active.

## Profitability validation

V2.11 counts one fully closed non-exploratory automated position as one validation sample.
Partial exits from the same position are combined, and manual paper trades are
excluded. The app displays paper validation as passed only when all of the
following are true:

- at least 30 complete automated positions;
- net P&L after modeled costs is positive;
- profit factor is at least 1.20;
- maximum account drawdown is no more than 12%.

This gate is deliberately labeled **paper validation**. Free public RPC and API
data can be delayed; simulated fills cannot fully reproduce MEV, failed swaps,
honeypots, rapid liquidity removal, or live execution latency.

## Monitoring and reports

The watch list, local snapshots, abnormal-move alerts, and CSV monitoring
report from V2.7 remain available. Simulation trades can also be exported to a
UTF-8 CSV file from the simulation page.

## Free application updates

The updater reads public releases from `martinekading-ctrl/-`. It requires a
Windows x64 ZIP and a matching `SHA256.txt`. Update installation remains
user-initiated.

The local update configuration is stored at
`%LocalAppData%\BaseTokenRadar\update_config_v26.json`; the older filename is
retained for compatibility.

## Build and test

```powershell
go test ./...
go vet ./...
go build -trimpath -ldflags="-H=windowsgui -s -w" -o ..\MultiChainTokenRadar.exe .
```

Unit coverage includes paper accounting, costs, stop/exit rules, automatic
entry gates, cross-chain identity, complete-position validation, partial-exit
grouping, loss-streak circuit breaking, migration behavior, monitoring, and
update helpers.
