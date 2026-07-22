# Multi-Chain Token Radar V2.6

Windows x64 desktop radar for Base, BSC, and Arbitrum. It discovers recent
liquidity pools, enriches market data with DexScreener and contract-risk fields
with GoPlus, and includes a local-only paper-trading account. It never connects
to a wallet, stores a private key, or submits a real transaction.

## Real-time data

The **5-second live monitor** remains enabled by default. It uses free public
RPC endpoints and the public DexScreener and GoPlus APIs. These services may
rate-limit or temporarily fail; the application keeps the last valid result and
shows a connection diagnostic option.

## Free application updates

V2.6 checks a configured public GitHub Releases repository at startup and then
at most once every six hours. The **Update** button checks immediately. If a
newer release is found, its label changes to **Install x.y.z**. Installation is
always user-initiated: the app downloads the Windows x64 zip, verifies it
against the separately published `SHA256.txt`, exits, replaces its executable,
and restarts.

No token, account, or paid service is required. GitHub must host a **public**
repository so a desktop app can download releases without embedding credentials.

Create this file in `%LocalAppData%\BaseTokenRadar\update_config_v26.json`:

```json
{
  "github_repository": "martinekading-ctrl/-"
}
```

Alternatively, set the `MCTR_UPDATE_REPOSITORY` environment variable to the
same `owner/repository` value. The file takes precedence when it has a value.

## Release contract

Publish a GitHub Release tagged as a normal semantic version, for example
`v2.6.1`. Attach both of these files:

- `MultiChainTokenRadar_V2.6.1_Windows_x64.zip` — contains
  `MultiChainTokenRadar.exe`.
- `SHA256.txt` — contains the SHA-256 hash of that zip, followed optionally by
  its filename.

The updater rejects releases without both assets, invalid version tags, update
files above 250 MiB, invalid zip files, and checksum mismatches.

## Build and test

```powershell
go test ./...
go build -trimpath -ldflags="-H=windowsgui -s -w" -o ..\MultiChainTokenRadar.exe .
```

Only the paper-trading engine and updater version/checksum helpers have unit
tests at present. Scanner, UI, and network-provider integration testing should
be the next engineering priority.
