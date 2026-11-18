package main

const placeDoc = `Station or place name ("Gare du Nord", "Place de la Bastille"), "lat,lon", ` +
	`or an id from a previous result.`

const timeDoc = `ISO 8601. Without an offset it's Paris time. More than an hour in the past is refused.`

const lineDoc = `M14, RER B, T3a, Bus 21, Transilien J, N14, or just 14 or B.`

const commonDoc = `
Île-de-France only, data from IDFM's PRIM. Names and disruption texts are in French.
resolved shows how a place was understood; snapped_from means it was replaced by the nearest station.
ambiguous_place / ambiguous_line: show the candidates, let the user pick, call again with that id. Never guess.
quota or auth: don't retry right away.`

const planJourneyDoc = `Public transport journeys from A to B, leaving at or arriving by a time.
Journeys are already sorted: present the first one.
Times are timetable times. first_leg_realtime, when set, is live data for the first ride: "trip" is that ` +
	`train, "direction" lists the next trains to the same terminus. Tell the user which times are live.
realtime_unavailable says why live data failed; those times are timetable.
Mention disruptions: service = faults, works, strikes; equipment = broken lifts where the trip boards, ` +
	`changes or alights.
Use profile step_free for luggage, a pushchair or a wheelchair.
Give the user citymapper_url.
To compare several destinations, use rank_destinations instead.` + commonDoc

const rankDoc = `Rank up to 20 destinations by door-to-door public transport time from one origin. ` +
	`Use it to pick the most convenient place (e.g. a doctor): one call, not plan_journey for each. ` +
	`Prefer coordinates over addresses.
Timetable times only. A destination that fails goes in failed with its error.` + commonDoc

const departuresDoc = `Next departures at a station, per line and direction.
realtime false means timetable times: say so. realtime_unavailable says why live data failed.
Without lines: up to 12 lines, rail first; the rest are in skipped_lines.` + commonDoc

const statusDoc = `Faults, works and strikes on 1 to 10 lines: in force now (active) or starting within ` +
	`days (upcoming). Broken lifts are not listed here; plan_journey has them.` + commonDoc
