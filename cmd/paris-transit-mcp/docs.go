package main

const placeDoc = `A place: a station or address name such as "Gare du Nord" or "12 rue de Rivoli Paris", ` +
	`"latitude,longitude" such as "48.8584,2.3470", or an id from a previous result or from candidates.`

const timeDoc = `ISO 8601; without an offset, such as 2026-11-20T08:30, it is Paris time. ` +
	`A time more than an hour in the past is refused.`

const lineDoc = `Names such as M14, Métro 14, RER B, T3a, Tram 3a, Bus 21, Transilien J, Noctilien N14, ` +
	`or a bare number or letter such as 14 or B.`

// commonDoc applies to every tool.
const commonDoc = `
Île-de-France public transport from Île-de-France Mobilités' PRIM data. Read-only. Place and stop names and ` +
	`disruption texts are PRIM's, in French; times are ISO 8601 Paris time.
Every result says in resolved what the server understood a place as; tell the user when it is not what they meant. ` +
	`resolved.snapped_from means the server replaced the place by a station that distance_m away.
Errors: a result with error.code ambiguous_place or ambiguous_line lists candidates: show them to the user, let ` +
	`them choose, then call again with the chosen candidate's id (or line_id); never pick a town or a line yourself. ` +
	`not_found and no_transit_route are answers to tell the user. auth, quota, upstream and bad_request are errors: ` +
	`quota means the daily PRIM quota is spent; do not retry a quota or auth error.`

const planJourneyDoc = `Door-to-door public transport journeys from one place to another (metro, RER, ` +
	`Transilien, tram, bus and walking), leaving at or arriving by a time.
journeys are already in the order to present: describe the first one, and the others as alternatives.
Times are timetable times, corrected for announced disruptions. first_leg_realtime, when not null, is live data ` +
	`for the first ride: match trip is that very train (expected, delay_min, status), match direction lists the next ` +
	`trains of the line towards the same terminus (next). Say which times are live and which are timetable.
disruptions.service are faults, works or strikes on the journey's lines; disruptions.equipment are broken lifts ` +
	`at the stations where the journey boards, changes or alights. Mention them.
profile step_free is for luggage, a pushchair or a wheelchair: it keeps routes that have lifts and puts first ` +
	`those without a broken lift. PRIM cannot count stairs.
citymapper_url opens the same trip in Citymapper for live navigation: give it to the user.
To compare several destinations by travel time, call rank_destinations once instead of this tool for each.
no_transit_route means PRIM found no public transport, even from the stations within 500 m of each end.` + commonDoc

const rankDoc = `Rank 1 to 20 destinations from one origin by door-to-door public transport time: duration, ` +
	`transfers, walking, lines and a Citymapper link for each. Use it to pick among several places by travel time, ` +
	`for example the most convenient practitioner: one call, never plan_journey for each. Pass coordinates when you ` +
	`have them, rather than address text.
Each destination may have its own arrive_by (such as an appointment time); the others leave at depart_at or now.
Times are timetable times, without realtime. ranked is sorted by duration, then transfers, then walking. A ` +
	`destination that fails, for example an ambiguous address, is in failed with its error and candidates; the ` +
	`others are still ranked.` + commonDoc

const departuresDoc = `Next departures at a station, by line and direction, live when PRIM has live data.
realtime true means the times are live; realtime false means PRIM has no live data for that line and the times ` +
	`are the timetable: say so. status is onTime, delayed, cancelled or noReport; aimed is the timetabled time ` +
	`when PRIM gives it.
Without lines, it returns up to 12 lines of the station, RER and trains first, then metro, tram, bus and ` +
	`Noctilien; skipped_lines lists the others, which a second call can ask for in lines.` + commonDoc

const statusDoc = `Service disruptions of 1 to 10 lines: faults, works and strikes in force now (active) and ` +
	`works that start within days days (upcoming), each with up to 5 periods, the nearest first. Broken lifts and ` +
	`other equipment faults are not here: plan_journey lists them for the stations of a journey.` + commonDoc
