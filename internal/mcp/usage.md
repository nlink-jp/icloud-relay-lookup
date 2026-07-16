# icloud-relay-lookup MCP — operating manual

This server reports whether an IP address is an **Apple iCloud Private Relay
egress IP**, answered offline from a locally cached copy of Apple's published
`egress-ip-ranges.csv`. A hit also carries the geo hints Apple publishes for
that egress range (country / ISO region / city) — i.e. roughly where the real
user behind the relay is. All checks are offline; only `update_list` touches
the network. **No credentials are required.**

Call `cache_status` first to confirm a list exists and is fresh. If it does
not exist, call `update_list` (no token needed).

Interpretation note: a Private Relay egress IP means the traffic came through
Apple's relay — the client is very likely a legitimate Apple-ecosystem user
who enabled Private Relay, not a Tor exit or an open proxy. The geo hints
locate the egress range's service area, not the exact user.

## Tools

### `get_usage`
Returns this manual. No arguments.

### `cache_status`
Reports `fetched`, `ranges` (with `v4`/`v6`), `etag`, `stale`, `age_hours`,
`source`, and the store `path`. No arguments. Returns an error result when no
list exists yet.

### `update_list`
Revalidates the local store against Apple's list using an ETag conditional
GET: an unchanged list answers `not_modified:true` and only bumps freshness
(nearly free); a changed list is downloaded (~12MB) and re-indexed. No
arguments, no credentials. The upstream format is unversioned, so a download
that no longer parses is rejected and the previous cache is kept
(`update failed: … format not recognized`).

### `check_ip`
IP → is it an iCloud Private Relay egress IP?
- Arguments: `ip` (string) **or** `ips` (array of strings). At least one required.
- Result: a JSON array, one object per input, each with `input` and
  `is_private_relay`. On a hit, `prefix` (the matched range) and the geo
  hints `country`, `region`, `city` are also present (city may be empty for
  a few thousand upstream rows). Invalid addresses come back with
  `is_private_relay:false` and `error:"invalid address"`.

## The list lifecycle

`update_list` downloads Apple's whole egress list once (~290k ranges) and
stores it locally; every `check_ip` is then an offline longest-prefix match.
Freshness lives inside the store (`fetched`), not the file mtime. Apple
serves the list with `cache-control: max-age=3600`, so the CLI auto-revalidates
past a TTL (1-hour floor) with a conditional GET. `cache_status` reports
`stale:true` once the copy is over 7 days old.

## Recovery table

| Symptom (result text) | What it means | What to do |
|---|---|---|
| `no local egress list …` | The list has not been downloaded | Call `update_list` |
| `check_ip` → `error:"invalid address"` | The input was not a valid IP | Fix the input |
| `is_private_relay:false` (no error) | Address is not a known Private Relay egress | Expected; no action |
| `is_private_relay:true`, empty `city` | Hit on a range without a city hint | Expected; `country`/`region` may still be set |
| `update failed: … format not recognized` | Apple changed the CSV format | Previous cache still answers; report/upgrade the tool |
| `update_list` → `not_modified:true` | Cached copy confirmed current (HTTP 304) | Expected; freshness was bumped |
| `cache_status` → `stale:true` | List older than 7 days | Call `update_list` to refresh |

## Attribution

Data: Apple iCloud Private Relay egress IP ranges
(https://mask-api.icloud.com/egress-ip-ranges.csv). The cached copy is local
and is not redistributed.
