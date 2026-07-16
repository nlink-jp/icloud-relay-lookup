# icloud-relay-lookup

Is an IP address an **Apple iCloud Private Relay egress IP**?
`icloud-relay-lookup` answers offline from a locally cached copy of Apple's
published
[`egress-ip-ranges.csv`](https://mask-api.icloud.com/egress-ip-ranges.csv)
(~290k ranges). Download the list once with `update` (or let it
auto-revalidate), then every `check` is an instant in-memory longest-prefix
match — no network, no credentials. A hit also carries the geo hints Apple
publishes for that egress range: country, ISO region, and city.

Traffic from a Private Relay egress carries a distinctive context — anonymized,
but most likely a legitimate user inside the Apple ecosystem — and deserves
different handling than a Tor exit or an open proxy. The Apple-side sibling of
[`tor-exit-lookup`](https://github.com/nlink-jp/tor-exit-lookup) (Tor exits),
[`asn-lookup`](https://github.com/nlink-jp/asn-lookup) (AS / country), and
[`abuse-lookup`](https://github.com/nlink-jp/abuse-lookup) (reputation).
Together they profile an IP from four angles, over both CLI pipes and MCP.

## Install

Homebrew (macOS, Apple Silicon — signed & notarized prebuilt binary):

```sh
brew install nlink-jp/tap/icloud-relay-lookup
```

Or grab a prebuilt binary for linux/amd64, linux/arm64, darwin/arm64, or
windows/amd64 from the
[releases page](https://github.com/nlink-jp/icloud-relay-lookup/releases).

To build from source (Go 1.25+):

```sh
git clone https://github.com/nlink-jp/icloud-relay-lookup
cd icloud-relay-lookup
make build          # → dist/icloud-relay-lookup
```

## Quick start

```sh
# 1. Download Apple's egress list (public endpoint, no auth; ~12MB):
icloud-relay-lookup update

# 2. Check an address:
icloud-relay-lookup check 172.224.226.34
# → 172.224.226.34 is an iCloud Private Relay egress IP  [Oxford, GB-EN, GB — 172.224.226.34/31]

icloud-relay-lookup check 8.8.8.8
# → 8.8.8.8 is not an iCloud Private Relay egress IP        (exit code 1)

# 3. Use the exit code in a script:
if icloud-relay-lookup check "$ip"; then
  echo "$ip is coming through Private Relay"
fi

# 4. Filter a log's IPs in bulk:
cut -f1 access.log | icloud-relay-lookup check --json | jq 'select(.is_private_relay)'
```

## Commands

| Command | Description |
|---------|-------------|
| `check <IP>...` | Report whether each IP is a Private Relay egress IP (reads stdin if no args) |
| `update` | Revalidate/download the egress list and rebuild the local store |
| `status` | Show the cached list's freshness, size, and ETag |
| `mcp` | Run as a local MCP server over stdio |
| `version` | Print the version |

### `check` modes and exit codes

A single positional IP in text mode uses the grep convention so it composes in
shell:

| Code | Meaning |
|------|---------|
| `0` | the IP **is** an iCloud Private Relay egress IP |
| `1` | the IP is **not** an iCloud Private Relay egress IP |
| `2` | error (invalid IP, no local list, …) |

Any other shape — multiple IPs, stdin input, or `--json` — is **batch mode**:
one result line per IP on stdout, and the exit code signals errors only
(`0` / `2`). `--json` emits one JSON object per line
(`{ip, is_private_relay, prefix?, country?, region?, city?, checked_at, list_fetched_at}`).

## Auto-refresh

Apple serves the list with an `ETag` and `cache-control: max-age=3600`. By
default, `check` revalidates when the cached list is older than the TTL
(default 1 hour, floored at 1 hour to match Apple's max-age) using a
conditional GET — an unchanged list answers `304 Not Modified` and costs
almost no bandwidth. If the revalidation fails (e.g. offline), the cached list
is used with a warning rather than failing. Disable per-call with
`--no-update`, or globally with `[apple] auto_update = false`.

The upstream CSV format is unofficial and unversioned; a download whose rows
no longer parse is rejected and the previous cache is kept.

## MCP server

`icloud-relay-lookup mcp` speaks JSON-RPC 2.0 over stdio (standard library
only). Tools: `check_ip`, `cache_status`, `update_list`, and `get_usage` (an
embedded operating manual; the server also advertises it via the initialize
`instructions` field). Example registration:

```json
{
  "mcpServers": {
    "icloud-relay-lookup": { "command": "icloud-relay-lookup", "args": ["mcp"] }
  }
}
```

## Configuration

No credentials are required — the endpoint is public. Everything has a
sensible default; override via a config file, environment variables, or flags.

```toml
# ~/.config/icloud-relay-lookup/config.toml
[apple]
# url = "https://mask-api.icloud.com/egress-ip-ranges.csv"
# ttl_minutes = 60      # auto-revalidation threshold (floored at 60)
# auto_update = true    # auto-revalidate on check when stale

[store]
# dir = "~/.local/share/icloud-relay-lookup"
```

| Setting | Env var | Flag | Default |
|---------|---------|------|---------|
| List URL | `ICLOUD_RELAY_LOOKUP_URL` | `--url` (update, mcp) | `…/egress-ip-ranges.csv` |
| Store dir | `ICLOUD_RELAY_LOOKUP_STORE_DIR` | `--store-dir` | `~/.local/share/icloud-relay-lookup` |
| TTL (minutes) | `ICLOUD_RELAY_LOOKUP_TTL_MINUTES` | — | `60` (min 60) |
| Auto-update | `ICLOUD_RELAY_LOOKUP_AUTO_UPDATE` | `--no-update` (off) | `true` |
| Config path | — | `-c`, `--config` | `~/.config/icloud-relay-lookup/config.toml` |

## How it works

`update` fetches the whole egress list (~290k CIDR ranges, IPv4 + IPv6) and
stores the CSV verbatim beside a small `meta.json` carrying the fetch time,
ETag, and counts (atomic temp + rename). `check` parses the cached CSV into a
hash map per distinct prefix length (the real list has ~19), so a lookup is at
most a handful of map probes, longest prefix first. Addresses are
canonicalized (`Unmap`) so v4-in-v6 inputs match. Freshness is stamped inside
`meta.json`, not the file mtime, so it survives copies; `status` warns once
the local copy is over 7 days old.

Note the direction of the signal: the list locates Apple's egress ranges and
their intended service areas. A hit tells you the connection came through
Private Relay near that area — it does not identify the user, which is the
point of Private Relay.

## Development

```sh
make test        # go test -race -cover ./...
make check       # lint + test + build-all
```

No external dependencies — standard library only. See
[docs/en/architecture.md](docs/en/architecture.md) for the design rationale.

## License

MIT — see [LICENSE](LICENSE). Egress-list data is published by
[Apple](https://developer.apple.com/support/prepare-your-network-for-icloud-private-relay/);
the cached copy is local and is not redistributed.
