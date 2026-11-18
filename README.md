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

1. Get a token from [PRIM](https://prim.iledefrance-mobilites.fr). If your browser's language is Chinese, the site
   reloads forever: switch it to English or French.
2. Install:

   ```sh
   go install github.com/tio-plato/paris-transit-mcp/cmd/paris-transit-mcp@latest
   # or
   nix run github:tio-plato/paris-transit-mcp
   ```

3. Set `PRIM_API_KEY` in the client's config.

Claude Code (`.mcp.json`):

```json
{
  "mcpServers": {
    "paris-transit": {
      "command": "paris-transit-mcp",
      "env": {"PRIM_API_KEY": "${PRIM_API_KEY}"}
    }
  }
}
```

Hermes (`config.yaml`):

```yaml
mcp_servers:
  paris-transit:
    command: paris-transit-mcp
    env:
      PRIM_API_KEY: ${PRIM_API_KEY}
```

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
