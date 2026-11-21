# paris-transit-mcp

An [MCP](https://modelcontextprotocol.io) server for public transport in Paris and Île-de-France, using the open
data from [PRIM](https://prim.iledefrance-mobilites.fr) (Île-de-France Mobilités). Read-only, stdio.

Not affiliated with IDFM. You need a free PRIM account and token.

## Tools

- `plan_journey`: A to B by metro, RER, train, tram and bus. Live times for the first ride, disruptions and
  broken lifts on the way, a Citymapper link. `profile: step_free` for luggage or a wheelchair.
- `rank_destinations`: sort up to 20 places by travel time from one origin, e.g. to pick the closest doctor.
- `next_departures`: next trains or buses at a station, live when PRIM has the data, timetable otherwise.
- `line_status`: faults, works and strikes on a line, now and in the coming days.

Places can be names, `lat,lon` or ids from earlier results. Times without an offset are Paris time.

A few things the server handles so the model doesn't have to:

- An address that exists in several towns ("12 rue Victor Hugo") comes back as `ambiguous_place` with candidates.
- If PRIM only finds a long walk, the server retries from the nearest station.
- Live times are matched to the actual train, or left out. When live data fails, `realtime_unavailable` says why.
- Lift faults are listed once per lift, and only where you board, change or get off.

## Setup

Give your agent this repository's URL and a PRIM token: it can follow the steps below on Linux, macOS or Windows,
x86_64 or ARM, without Go or Nix.

1. Get a token from [PRIM](https://prim.iledefrance-mobilites.fr). If your browser's language is Chinese, the site
   reloads forever: switch it to English or French.
2. Install the program (below).
3. Add it to the client's MCP config (below).

### Install

Each release has one archive per system, `paris-transit-mcp_<os>_<arch>.tar.gz` (`.zip` on Windows), where `<os>` is
`linux`, `darwin` or `windows` and `<arch>` is `amd64` for x86_64 or `arm64` for aarch64 and arm64. The commands
below pick the archive, download it and `checksums.txt` from the latest release, stop if the checksum doesn't match,
and print the program's absolute path.

Linux and macOS, into `~/.local/bin`:

```sh
sh -eu <<'EOF'
case $(uname -s) in Linux) os=linux ;; Darwin) os=darwin ;; *) echo "unsupported: $(uname -s)" >&2; exit 1 ;; esac
case $(uname -m) in x86_64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; *) echo "unsupported: $(uname -m)" >&2; exit 1 ;; esac
file=paris-transit-mcp_${os}_${arch}.tar.gz
url=https://github.com/tio-plato/paris-transit-mcp/releases/latest/download
tmp=$(mktemp -d)
cd "$tmp"
curl -fsSLO "$url/$file"
curl -fsSLO "$url/checksums.txt"
line=$(grep "  $file\$" checksums.txt) || { echo "$file is not in checksums.txt" >&2; exit 1; }
if command -v sha256sum >/dev/null; then
  echo "$line" | sha256sum -c -
else
  echo "$line" | shasum -a 256 -c -
fi
mkdir -p "$HOME/.local/bin"
tar -xzf "$file" -C "$HOME/.local/bin" paris-transit-mcp
rm -rf "$tmp"
echo "$HOME/.local/bin/paris-transit-mcp"
EOF
```

Windows (PowerShell), into `%LOCALAPPDATA%\Programs\paris-transit-mcp`:

```powershell
& {
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue'
  $arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
  $file = "paris-transit-mcp_windows_$arch.zip"
  $url = 'https://github.com/tio-plato/paris-transit-mcp/releases/latest/download'
  $dir = "$env:LOCALAPPDATA\Programs\paris-transit-mcp"
  $tmp = New-Item -ItemType Directory -Path (Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid()))
  Invoke-WebRequest "$url/$file" -OutFile "$tmp\$file" -UseBasicParsing
  Invoke-WebRequest "$url/checksums.txt" -OutFile "$tmp\checksums.txt" -UseBasicParsing
  $line = Get-Content "$tmp\checksums.txt" | Where-Object { $_ -match "^[0-9a-f]{64}  $([regex]::Escape($file))$" }
  if (-not $line) { throw "$file is not in checksums.txt" }
  if ((Get-FileHash "$tmp\$file" -Algorithm SHA256).Hash -ne $line.Split(' ')[0]) { throw "checksum mismatch: $file" }
  Expand-Archive "$tmp\$file" -DestinationPath $dir -Force -ErrorAction Stop
  Remove-Item $tmp -Recurse
  "$dir\paris-transit-mcp.exe"
}
```

To update, run the same commands again and restart the client.

With Go or Nix, these work too:

```sh
go install github.com/tio-plato/paris-transit-mcp/cmd/paris-transit-mcp@latest   # latest release
nix run github:tio-plato/paris-transit-mcp                                         # follows main
```

### Configure

Use the absolute path the install printed: the client doesn't always have `~/.local/bin` in its `PATH`. Keep the
token where the client keeps its secrets, not in the config.

Hermes: add this to `~/.hermes/config.yaml` and put `PRIM_API_KEY=<token>` in `~/.hermes/.env`. Hermes passes only
the variables listed under `env`, so keep that line.

```yaml
mcp_servers:
  paris-transit:
    command: /path/to/paris-transit-mcp
    env:
      PRIM_API_KEY: ${PRIM_API_KEY}
```

OpenClaw: add this to `~/.openclaw/openclaw.json`, put `PRIM_API_KEY=<token>` in `~/.openclaw/.env`, then run
`openclaw gateway restart` so the gateway reads it.

```json
{
  "mcp": {
    "servers": {
      "paris-transit": {
        "command": "/path/to/paris-transit-mcp",
        "env": {"PRIM_API_KEY": "${PRIM_API_KEY}"}
      }
    }
  }
}
```

Claude Code: add this to `.mcp.json` and set `PRIM_API_KEY` in the environment Claude Code starts from.

```json
{
  "mcpServers": {
    "paris-transit": {
      "command": "/path/to/paris-transit-mcp",
      "env": {"PRIM_API_KEY": "${PRIM_API_KEY}"}
    }
  }
}
```

Other clients take the same `mcpServers` JSON. If a client doesn't expand `${…}`, write the token itself there; the
config file then holds a secret. If the server gets the placeholder unexpanded, it stops at startup and says so.

The default quota is 1000 requests a day per API, plenty for one person. You can raise it for free on PRIM.

## Limits

- Journey times come from the timetable. Only the first ride gets live data.
- Live data covers about the next three hours, and not every stop.
- RATP works often only show up as disruption messages, not in the timetable.
- PRIM can't count stairs, and lists a lot of lift faults that look stale.

## Development

```sh
nix develop
nix flake check   # build, tests, lint
```

Tests replay recorded PRIM responses; they don't call PRIM.

## License

MIT. The recorded responses in `testdata/` are IDFM data under their own licences, see
[`testdata/NOTICE`](testdata/NOTICE).
