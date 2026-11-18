# paris-transit-mcp

A read-only [MCP](https://modelcontextprotocol.io) server for public transport in Île-de-France (Paris and its
region), on the open data of [Île-de-France Mobilités' PRIM platform](https://prim.iledefrance-mobilites.fr).
An agent can plan door-to-door journeys by metro, RER, Transilien, tram, bus and on foot, rank several destinations
by travel time, read the next departures at a station, live when PRIM has live data, and check a line's faults and
planned works.

It works with any MCP client over stdio: Claude Code, Claude Desktop, Hermes and others.

The server makes the decisions a model would otherwise have to guess: it notices an address that exists in several
towns and asks which one, matches live data to the very train a journey takes, orders the journeys, replaces a place
PRIM cannot reach on foot by the nearest station, and says which times are live and which are timetable.

> Not affiliated with Île-de-France Mobilités. You need your own PRIM account and token (free).

## Tools

| Tool | What it returns | PRIM requests |
|---|---|---|
| `plan_journey` | The journeys PRIM proposes from A to B, often 2 to 5, leaving at or arriving by a time, in the order to present: rides, transfers, walking, faults and works on the lines, broken lifts at the stations where the journey boards, changes or alights, live data for the first ride and a Citymapper link | 1, plus place lookups; 1 per first ride leaving within 90 minutes; 2 to 3 more when PRIM finds only a long walk |
| `rank_destinations` | 1 to 20 destinations from one origin, sorted by door-to-door time, each with transfers, walking, lines and a Citymapper link | 1 per destination, plus place lookups |
| `next_departures` | The next departures at a station, by line and direction, live when PRIM has live data, else from the timetable and marked so | 1 for the station's lines, plus 1 to 2 per line |
| `line_status` | Faults, works and strikes of 1 to 10 lines, in force now or starting within a number of days | 1 per line, plus 1 per line name |

Parameters:

- A place is a station, place or address name (`Gare du Nord`, `Place de la Bastille`), `latitude,longitude`
  (`48.8584,2.3470`) or an id from a previous result. Every result says in `resolved` what the place was understood
  as.
- Times are ISO 8601. Without an offset, `2026-11-20T08:30` is Paris time. A time more than an hour in the past is
  refused, as it is most likely a wrong date.
- Lines are names such as `M14`, `RER B`, `T3a`, `Bus 21`, `Transilien J`, `Noctilien N14`, a bare `14` or `B`, or
  line ids.

What the server decides:

- **Ambiguous addresses.** `12 rue Victor Hugo` exists in many towns and PRIM lists Montreuil first. When the first
  results hold the same address in several towns and the query names none of them, the result is `ambiguous_place`
  with the candidates, for the user to choose. Station names take PRIM's first result, which is the Paris station.
- **Ambiguous lines.** `14` is metro 14 and a bus 14: a bare number or letter takes the one rail line among the
  matches, else the result is `ambiguous_line` with the candidates. `RER B` never matches the RER B replacement bus.
- **Live data for the first ride.** Journeys are timetable times corrected for announced disruptions. When the first
  ride leaves within 90 minutes, the server asks SIRI for that stop and line and matches the very train by its number
  and timetabled time (`match: trip`); otherwise it lists the next trains towards the same terminus that can still be
  caught (`match: direction`); otherwise it gives nothing rather than another train's time. A journey whose train is
  cancelled goes last.
- **Step-free journeys.** `profile: step_free` asks PRIM for lift-only routes and puts first those without a broken
  lift at the stations where the journey boards, changes or alights, then fewer transfers, less walking, shorter
  time. PRIM's luggage profile changes nothing, and PRIM cannot count stairs.
- **Walk-only answers.** A point PRIM cannot reach, such as inside the Forum des Halles, gets only a walk of three
  hours. When PRIM finds no public transport and only a walk over 30 minutes, the server replaces each end that is
  not a station by the nearest station within 500 m and asks again; if that fails too, the result is
  `no_transit_route`, never the walk. A walk over 30 minutes that PRIM proposes beside public transport is left out.
  The Citymapper link still starts and ends where the user asked.
- **Departures by line.** PRIM's departures for a whole station are capped at about 175 and silently drop lines at
  big stations, so the server asks for each line. Without lines it takes up to 12 lines of the station, RER and
  trains first, and lists the others in `skipped_lines`.
- **Equipment faults.** PRIM publishes broken lifts as disruptions with the severity of a line fault. They are told
  apart by their tag: `plan_journey` lists them under `equipment`, each lift once although PRIM republishes the
  same fault many times; `line_status` leaves them out.

Errors carry `{"error": {"code", "message"}}`. `auth`, `quota`, `upstream` and `bad_request` are MCP errors
(`isError`); `not_found`, `no_transit_route`, `ambiguous_place` and `ambiguous_line` are answers to the query.

## Get a PRIM token

1. Create an account on [PRIM](https://prim.iledefrance-mobilites.fr). If your browser's first language is Chinese,
   the site reloads endlessly: switch the browser to English or French first.
2. In your account, generate an API token. One token reaches every API this server uses: the Navitia journey planner,
   its traffic messages and the SIRI Lite next departures.
3. Default quotas are 1000 requests a day and 5 a second per API. A ranking of 20 destinations takes about 41
   requests, so personal use stays far below. The quotas can be raised for free on the "My API usage" page.

## Install

```sh
go install github.com/tio-plato/paris-transit-mcp/cmd/paris-transit-mcp@latest
```

or, with Nix:

```sh
nix run github:tio-plato/paris-transit-mcp
```

## Configuration

| Variable | Effect |
|---|---|
| `PRIM_API_KEY` | Your PRIM token. Required: the server stops at start without it, or when it holds a `${…}` reference the client left unexpanded. It is sent only in the `apiKey` header, never logged or returned. |

Requests are paced at 4 a second per API, below PRIM's limit of 5. Nothing is retried or cached: a retry spends
quota, and the client can call again.

## Clients

The server is named `paris-transit`, so the tools appear as `mcp__paris-transit__plan_journey` and so on.

Claude Code, in `.mcp.json`, with the token taken from the environment that runs `claude`:

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

Hermes, in `config.yaml`:

```yaml
mcp_servers:
  paris-transit:
    command: paris-transit-mcp
    env:
      PRIM_API_KEY: ${PRIM_API_KEY}
```

## What PRIM cannot tell

- RATP's planned works do not change the timetable; they are only published as disruption messages. A journey may
  still go through a closed station: read its `disruptions.service`.
- Live data covers about the next three hours, and not every stop.
- There is no live vehicle position.
- The journey planner's times are timetable times, with announced disruptions but without delays.
- Station-to-station walks inside a station have a duration but no step count.

## Data

Journeys, departures and disruptions come from [Île-de-France Mobilités](https://www.iledefrance-mobilites.fr)
through [PRIM](https://prim.iledefrance-mobilites.fr), under the licences of each API: the Mobility License (Licence
Mobilités) for the journey planner and its traffic messages, and, for SIRI Lite next departures, the licence its
page names. The responses recorded in `testdata/` for the tests are PRIM data too; `testdata/NOTICE` says which
licence covers them.

## Development

```sh
nix develop       # Go, gopls, golangci-lint
nix flake check   # build, tests, golangci-lint
```

Tests replay recorded PRIM responses from a local fake server; they never reach PRIM.
