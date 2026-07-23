# Multi-Chain Token Radar V2.18

V2.18 is a Windows x64 research application for Ethereum, Base, BSC, Optimism,
Polygon, and Arbitrum. Its
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

## V2.18 stable Simplified-Chinese market pages

- **中文行情** now opens the exact pool on GeckoTerminal's Simplified Chinese
  interface instead of depending on an OKLink token page. The new link was
  checked against live BSC and Base pools and keeps the precise pool address,
  price chart, liquidity, transactions, and age together.
- The link is generated only when the scanner has an exact valid pool address.
  When that evidence is unavailable, the application does not search by token
  symbol or send the user to a possibly different token; it clearly asks the
  user to use **原始走势** instead.
- CSV exports use the same exact-pool Chinese-market URL. The original
  DexScreener link and chain explorer remain separate reference links.

## V2.17 1,000-USDC validation account and staged exits

- A newly reset paper account starts with **1,000 USDC**, uses a maximum
  **20-USDC** allocation per strict candidate, permits at most three open
  positions, and pauses new entries after a 30-USDC realized daily loss.
- The dedicated **验证** profile still requires strict market-first eligibility:
  exact recent pool, no older indexed DEX market, complete public security
  verification, known tax at or below 5%, and the early market gates. It adds
  a short public-price stability observation before a paper entry.
- The exit plan models a net 12% hard stop, sells 35% of the original position
  at 30%, another 35% at 60%, then lets the final 30% target 100% or exit on
  an 18% drawdown from its high. Security and liquidity emergency exits remain
  active and take priority.
- This is a forward paper-validation plan, not a promise of a 100% return.
  The app has no complete historical record of past pool liquidity, security
  fields, and executable quotes, so it does not claim a fabricated historical
  backtest. It collects new timestamped paper observations from this reset
  account. Validation now requires 100 complete automated positions, positive
  net P&L after modeled costs, profit factor of at least 1.30, and maximum
  drawdown no greater than 15%.

## V2.16 strict signals and a read-only review queue

- The radar is no longer blank merely because no token has cleared every
  trading gate. It presents two explicitly separate layers: **strict signals**
  first and a compact **read-only review queue** beneath them.
- A strict signal is still the only row that can start a manual or automatic
  paper position. A review row has `PotentialEligible=false`, is visibly
  labelled `【审查】`, displays the precise hold/rejection stage, and is rejected
  by every paper-buy and shadow-sample path.
- The review queue retains only recent, explicitly staged candidates (up to 30
  when there are no strict signals, or 12 alongside strict signals). It exists
  for inspecting a contract and its chart, not for relaxing entry rules or
  creating activity for its own sake. Expired cache entries remain hidden.
- The radar cards separately report strict signals, candidates awaiting exact
  market indexing, review rows, and the full local candidate cache. Thus
  `严格信号 = 0` means “do not trade”, while a nonzero review queue means the
  scanner is still collecting auditable evidence.

## V2.15 candidate funnel, risk snapshots, and review lane

- The radar cards now expose the latest funnel: strict candidates shown,
  candidates that are waiting for exact-pool indexing, and candidates rejected
  in the current scan with the leading reasons.
- A transition into or out of strict observation creates a visible local alert.
  The application also compares successive public risk snapshots for strict
  candidates and alerts on material liquidity loss, LP-lock reduction,
  creator-share growth, or top-holder concentration growth.
- Candidate details now show public GoPlus fields for LP-lock status,
  creator share, largest holder share, and holder count. Missing data remains
  explicitly marked as unverified rather than being treated as safe.
- Rejected but priced, non-hard-risk candidates enter a separate 60-minute
  **funnel review** lane. It records later public price/liquidity movement so
  the user can learn which gates skipped moves. It never allocates paper cash,
  opens a position, or contributes to profitability validation.

These are public-data diagnostics, not proof that LP is permanently safe or
that a creator cannot act through another address. They are intended to make
the filter auditable and improve future parameter decisions without lowering
the strict entry gate.

## V2.14 strict market-first research queue

The radar no longer treats every newly-created DEX pool as a new token. Its
visible list and paper-automation input are now restricted to candidates that
pass every public-data gate below:

1. a supported DEX factory emits a pool event with one known quote asset;
2. DexScreener indexes the **exact emitted pool address** and supplies a USD
   price plus its creation time; the application never substitutes a larger
   historical pool for the emitted pool;
3. the exact pool is no older than 60 minutes and the same token has no
   earlier indexed DEX market pool;
4. exact-pool liquidity is at least 10,000 USD, with at least four 1-hour buy
   transactions and no more sells than buys;
5. GoPlus returns a complete safe result: source is open, no honeypot,
   blacklist, cannot-sell, balance-change, self-destruct, proxy, or minting
   flag; buy/sell tax is known and at most 5%; creator/owner share is at most
   20%; and at least 20 holders are reported.

Candidates waiting for indexing or failing any one gate are retained in the
local re-check cache and may appear as visibly read-only review rows with their
specific reason. They cannot start a paper trade or shadow sample. The
no-earlier-pool check is a **market-first proxy**, not proof of a contract's
original deployment time. It is designed to exclude the practical failure mode
where an old token opens a fresh pool.

The 1-hour buy/sell figures are public aggregate transaction counts, not proof
that each buy came from an independent wallet. The application remains
paper-only: no wallet, private key, signing, or live order support exists.

## V2.13 six-chain EVM coverage and chain-aware paper costs

- Added direct new-pool discovery for **Ethereum**, **Optimism**, and
  **Polygon PoS**, bringing the monitored universe to six EVM chains.
- Ethereum uses Uniswap V2/V3, Optimism uses Uniswap V3, and Polygon uses
  QuickSwap V2 plus Uniswap V3. Each module has two or more RPC endpoints, known
  quote assets, DEX Screener enrichment, and GoPlus token-security checks.
- The scanner now gives each chain an independent candidate budget and can
  retain up to 80 recent candidates per chain (480 total).
- New-pool logs are read from a small confirmed-block window, so a public RPC
  that has reported a head before its log index is ready cannot silently skip a
  pool. BSC uses a 10-block 1RPC window that stays within its public limit.
- If a chain's new-pool log scan is incomplete, the UI reports that chain as
  degraded instead of claiming an all-chain healthy heartbeat.
- Paper trading now uses conservative minimum gas estimates by chain. A 1-USDC
  exploration sample that cannot cover Ethereum costs becomes a shadow
  research sample instead of a misleading simulated fill.

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

- at least 100 complete automated positions;
- net P&L after modeled costs is positive;
- profit factor is at least 1.30;
- maximum account drawdown is no more than 15%.

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
