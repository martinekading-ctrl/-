# Multi-Chain Token Radar V2.7

V2.7 is a Windows x64 research and monitoring application for Base, BSC, and
Arbitrum. It combines new-pool discovery, DexScreener market data, GoPlus risk
fields, a local paper account, and an auditable watch-list workflow.

It does not connect to a wallet, store private keys, submit transactions, or
promise trading returns.

## Trusted monitoring workflow

1. Select a token and choose **加入关注**.
2. The token remains in subsequent scans even when it is no longer a newly
   discovered candidate.
3. At most once every 30 seconds, the app records a local snapshot containing
   observed time, upstream update time, chain, contract, pool, block, price,
   liquidity, volume, score, security state, and source.
4. The app records rate-limited alerts for price moves of 10% or more,
   liquidity drops of 20% or more, security-state changes, score drops of 15
   points or more, missing results, and source data older than five minutes.
5. Choose **监控报告** to export snapshots and alerts as a UTF-8 CSV file.

These records make the data inspectable; they do not make third-party data
infallible. Important findings still need manual verification against an
explorer, DEX, and contract source.

## Real-time data

The five-second monitor uses free public RPC endpoints and public DexScreener
and GoPlus APIs. It preserves the last valid result when providers fail and
offers connection diagnostics. Public services can rate-limit or delay data.

## Free application updates

The update source is configured as `martinekading-ctrl/-`. V2.7 checks public
GitHub Releases at startup and at most every six hours. Installation is always
user-initiated and requires a release ZIP plus `SHA256.txt`.

Local update configuration:

```json
{
  "github_repository": "martinekading-ctrl/-"
}
```

The file location is `%LocalAppData%\BaseTokenRadar\update_config_v26.json`;
the V2.6 filename is retained so existing installations continue updating.

## Build and test

```powershell
go test ./...
go build -trimpath -ldflags="-H=windowsgui -s -w" -o ..\MultiChainTokenRadar.exe .
```

V2.7 adds unit coverage for watch-list persistence behavior, snapshot creation,
alert thresholds, cooldown behavior, paper trading, and updater helpers.
