package transit_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/tio-plato/paris-transit-mcp/internal/transit"
)

func plan(origin, destination string) transit.JourneyInput {
	return transit.JourneyInput{
		Origin: origin, Destination: destination, Datetime: "", DatetimeRepresents: "", Profile: "",
	}
}

func TestWalkOnlyOriginIsReplacedByTheNearestStation(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	e.fake.places(t)
	e.fake.sequence(t, navitia+"journeys",
		"20261118T0521-journey-forum-des-halles-la-defense.json",
		"20261118T0521-journey-chatelet-la-defense.json")
	e.fake.file(t, navitia+"coords/*", "20261118T0521-nearby-forum-des-halles.json")

	out, err := e.svc.PlanJourney(t.Context(), plan(forum, "La Défense"))
	if err != nil {
		t.Fatal(err)
	}

	if out.Error != nil {
		t.Fatalf("error %+v", out.Error)
	}
	o := out.Origin.Resolved
	if o.ID != chateletLH || o.SnappedFrom == nil || o.SnappedFrom.Name != forum || o.SnappedFrom.DistanceM != 17 {
		t.Errorf("origin %+v (snapped from %+v), want Châtelet - Les Halles, 17 m from %s", o, o.SnappedFrom, forum)
	}
	if len(out.Journeys) == 0 || len(out.Journeys[0].Legs) == 0 {
		t.Fatalf("first journey %+v has no public transport", out.Journeys)
	}
	reqs := e.fake.requestsTo(navitia + "journeys")
	if len(reqs) != 2 || reqs[1].Query.Get("from") != chateletLH || reqs[1].Query.Get("to") != laDefense {
		t.Errorf("journeys requests %+v, want a second one from Châtelet - Les Halles to La Défense", reqs)
	}
}

func TestWalkOnlyAfterSnappingIsNoTransitRoute(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	e.fake.places(t)
	e.fake.file(t, navitia+"journeys", "20261118T0521-journey-forum-des-halles-la-defense.json")
	e.fake.file(t, navitia+"coords/*", "20261118T0521-nearby-forum-des-halles.json")

	out, err := e.svc.PlanJourney(t.Context(), plan(forum, "La Défense"))
	if err != nil {
		t.Fatal(err)
	}

	if out.Error == nil || out.Error.Code != transit.CodeNoTransitRoute {
		t.Fatalf("error = %+v, want no_transit_route", out.Error)
	}
	if len(out.Journeys) != 0 {
		t.Errorf("returned the walk %+v as an answer", out.Journeys)
	}
}

// rerB serves the Gare du Nord → Massy-Palaiseau journey, whose first ride is
// train PLIC06 at 05:35, and the RER B departures at Gare du Nord recorded
// with it.
func rerB(t *testing.T, siriFixture string) *env {
	t.Helper()
	e := newEnv(t, at(t, "20261118T0520"))
	e.fake.places(t)
	e.fake.file(t, navitia+"journeys", "20261118T0520-journey-gare-du-nord-massy.json")
	if siriFixture != "" {
		e.fake.file(t, siri, siriFixture)
	}
	return e
}

func TestFirstRideMatchesTheSameTrain(t *testing.T) {
	t.Parallel()
	e := rerB(t, "20261118T0520-siri-gare-du-nord-rer-b.json")

	out, err := e.svc.PlanJourney(t.Context(), plan("Gare du Nord", "Massy-Palaiseau"))
	if err != nil {
		t.Fatal(err)
	}

	rt := out.Journeys[0].FirstLegRealtime
	if rt == nil || rt.Match != "trip" {
		t.Fatalf("first_leg_realtime = %+v, want a trip match", rt)
	}
	// SIRI expects PLIC06 at 04:35:02 UTC.
	if rt.Expected != "2026-11-18T05:35:02+01:00" || rt.Status != "onTime" {
		t.Errorf("first_leg_realtime = %+v, want PLIC06 expected at 05:35:02, onTime", rt)
	}
	reqs := e.fake.requestsTo(siri)
	if len(reqs) != 1 || reqs[0].Query.Get("MonitoringRef") != "STIF:StopArea:SP:71410:" ||
		reqs[0].Query.Get("LineRef") != "STIF:Line::C01743:" {
		t.Errorf("SIRI requests %+v, want one for Gare du Nord and RER B", reqs)
	}
}

func TestTrainWithAnotherNumberIsNotMatchedByTimeAlone(t *testing.T) {
	t.Parallel()
	// In this copy PLIC06 is renamed ZZZZ06, and INCA07, which runs the other
	// way to Mitry - Claye, is moved to 05:35, the minute of the leg.
	e := rerB(t, "20261118T0520-siri-gare-du-nord-rer-b-edited-trip-number.json")

	out, err := e.svc.PlanJourney(t.Context(), plan("Gare du Nord", "Massy-Palaiseau"))
	if err != nil {
		t.Fatal(err)
	}

	rt := out.Journeys[0].FirstLegRealtime
	if rt == nil {
		return
	}
	if rt.Match != "direction" {
		t.Fatalf("first_leg_realtime = %+v, want no trip match", rt)
	}
	// The trains to Saint-Rémy-lès-Chevreuse from 05:33 on.
	want := []string{"2026-11-18T05:35:02+01:00", "2026-11-18T05:53:00+01:00", "2026-11-18T06:08:00+01:00"}
	if len(rt.Next) != len(want) {
		t.Fatalf("next = %+v, want %v", rt.Next, want)
	}
	for i, n := range rt.Next {
		if n.Time != want[i] {
			t.Errorf("next[%d] = %+v, want %s", i, n, want[i])
		}
	}
}

func TestFailedRealtimeLeavesTheJourney(t *testing.T) {
	t.Parallel()
	e := rerB(t, "")
	e.fake.status(siri, http.StatusServiceUnavailable)

	out, err := e.svc.PlanJourney(t.Context(), plan("Gare du Nord", "Massy-Palaiseau"))
	if err != nil {
		t.Fatal(err)
	}

	if out.Error != nil || len(out.Journeys) != 1 {
		t.Fatalf("result %+v, want the journey", out)
	}
	if rt := out.Journeys[0].FirstLegRealtime; rt != nil {
		t.Errorf("first_leg_realtime = %+v, want null", rt)
	}
}

func TestNoRealtimeForARideBeyondNinetyMinutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0400")) // the ride leaves at 05:35
	e.fake.places(t)
	e.fake.file(t, navitia+"journeys", "20261118T0520-journey-gare-du-nord-massy.json")
	e.fake.file(t, siri, "20261118T0520-siri-gare-du-nord-rer-b.json")

	out, err := e.svc.PlanJourney(t.Context(), plan("Gare du Nord", "Massy-Palaiseau"))
	if err != nil {
		t.Fatal(err)
	}

	if rt := out.Journeys[0].FirstLegRealtime; rt != nil {
		t.Errorf("first_leg_realtime = %+v, want null", rt)
	}
	if n := len(e.fake.requestsTo(siri)); n != 0 {
		t.Errorf("sent %d SIRI requests", n)
	}
}

func TestStepFreeAsksForWheelchairRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	e.fake.places(t)
	e.fake.file(t, navitia+"journeys", "20261118T0521-journey-gare-du-nord-orly-step-free.json")
	in := plan("Gare du Nord", "Aéroport d'Orly")
	in.Profile = "step_free"

	_, err := e.svc.PlanJourney(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	reqs := e.fake.requestsTo(navitia + "journeys")
	if len(reqs) != 1 || reqs[0].Query.Get("wheelchair") != "true" {
		t.Errorf("journeys requests %+v, want wheelchair=true", reqs)
	}
}

func TestStepFreePutsBrokenLiftsLast(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	journeys := journeysJSON(t, []handJourney{
		{kind: "best", transfers: 1, walking: 60, durationS: 1500, rides: []ride{
			{
				from:        gareDuNord,
				to:          chateletLH,
				dep:         "20261119T083000",
				code:        "B",
				trip:        "",
				disruptions: []string{"lift"},
			},
			{from: chateletLH, to: orly, dep: "20261119T084000", code: "14", trip: "", disruptions: nil},
		}},
		{kind: "fastest", transfers: 2, walking: 60, durationS: 1800, rides: []ride{
			{from: gareDuNord, to: massy, dep: "20261119T083000", code: "B", trip: "", disruptions: nil},
		}},
		{kind: "comfort", transfers: 1, walking: 120, durationS: 2400, rides: []ride{
			{from: gareDuNord, to: orly, dep: "20261119T083000", code: "B", trip: "", disruptions: nil},
		}},
	}, `[`+liftOutage("lift", chateletLH)+`]`)
	e.fake.handle(navitia+"journeys", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, journeys) })
	in := plan("48.8809,2.3556", "48.7289,2.3610")
	in.Profile = "step_free"

	out, err := e.svc.PlanJourney(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	var kinds []string
	for _, j := range out.Journeys {
		kinds = append(kinds, j.Kind)
	}
	// No broken lift first, the fewer transfers first; the broken lift at the
	// change of the best journey puts it last.
	if len(kinds) != 3 || kinds[0] != "comfort" || kinds[1] != "fastest" || kinds[2] != "best" {
		t.Errorf("order %v, want comfort, fastest, best", kinds)
	}
}

func TestCitymapperLinkTakesLatitudeFirstAndOnlyAnArrivalTime(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	e.fake.places(t)
	e.fake.file(t, navitia+"journeys", "20261118T0521-journey-gare-du-nord-orly.json")

	for _, represents := range []string{"departure", "arrival"} {
		in := plan("Gare du Nord", "Aéroport d'Orly")
		in.Datetime, in.DatetimeRepresents = "2026-11-19T09:15:00+01:00", represents
		out, err := e.svc.PlanJourney(t.Context(), in)
		if err != nil {
			t.Fatal(err)
		}
		link, err := url.Parse(out.Journeys[0].CitymapperURL)
		if err != nil {
			t.Fatal(err)
		}
		q := link.Query()
		// Gare du Nord is at 48.880957 N, 2.355687 E in the places fixture.
		if q.Get("startcoord") != "48.880957,2.355687" || q.Get("endcoord") != "48.728894,2.361057" {
			t.Errorf(
				"%s: coordinates %q → %q, want latitude,longitude",
				represents,
				q.Get("startcoord"),
				q.Get("endcoord"),
			)
		}
		arrival := q.Get("arrival_time")
		if represents == "departure" && q.Has("arrival_time") {
			t.Errorf("departure: arrival_time = %q, want none", arrival)
		}
		if represents == "arrival" && arrival != "2026-11-19T09:15:00+01:00" {
			t.Errorf("arrival: arrival_time = %q, want 2026-11-19T09:15:00+01:00", arrival)
		}
	}
}

func TestJourneyTimesAreParisTime(t *testing.T) {
	t.Parallel()
	e := rerB(t, "20261118T0520-siri-gare-du-nord-rer-b.json")

	out, err := e.svc.PlanJourney(t.Context(), plan("Gare du Nord", "Massy-Palaiseau"))
	if err != nil {
		t.Fatal(err)
	}

	j := out.Journeys[0]
	if j.Departure != "2026-11-18T05:35:00+01:00" || j.Legs[0].Departure != "2026-11-18T05:35:00+01:00" {
		t.Errorf("departure %q, leg departure %q, want 2026-11-18T05:35:00+01:00", j.Departure, j.Legs[0].Departure)
	}
}

// gareDuNordNow serves Gare du Nord → Orly asked at 05:57: RER D train 153602
// then metro 14, or metro 4 then metro 14; and the SIRI of RER D and metro 4
// at Gare du Nord recorded with it.
func gareDuNordNow(t *testing.T, rerD string) *env {
	t.Helper()
	e := newEnv(t, at(t, "20261118T0557"))
	e.fake.places(t)
	e.fake.file(t, navitia+"journeys", "20261118T0557-journey-gare-du-nord-orly-now.json")
	e.fake.byQuery(t, siri, "LineRef", map[string]string{
		"STIF:Line::C01728:": rerD,
		"STIF:Line::C01374:": "20261118T0557-siri-gare-du-nord-m4.json",
	})
	return e
}

func TestMetroFirstRideMatchesTrainsToTheSameTerminus(t *testing.T) {
	t.Parallel()
	e := gareDuNordNow(t, "20261118T0557-siri-gare-du-nord-rer-d.json")

	out, err := e.svc.PlanJourney(t.Context(), plan("Gare du Nord", "Aéroport d'Orly"))
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Journeys) != 2 || out.Journeys[1].Legs[0].Line != "4" {
		t.Fatalf("journeys %+v, want RER D then metro 4", out.Journeys)
	}
	rt := out.Journeys[1].FirstLegRealtime
	if rt == nil || rt.Match != "direction" {
		t.Fatalf("metro 4: first_leg_realtime = %+v, want a match by direction", rt)
	}
	// The ride leaves at 06:00:30 towards Bagneux - Lucie Aubrac: the trains
	// there from 05:58:30 on, none to Porte de Clignancourt.
	want := []string{"2026-11-18T05:59:41+01:00", "2026-11-18T06:05:08+01:00", "2026-11-18T06:10:08+01:00"}
	if len(rt.Next) != len(want) {
		t.Fatalf("next = %+v, want %v", rt.Next, want)
	}
	for i, n := range rt.Next {
		if n.Time != want[i] {
			t.Errorf("next[%d] = %+v, want %s", i, n, want[i])
		}
	}
}

func TestJourneyWhoseFirstTrainIsCancelledGoesLast(t *testing.T) {
	t.Parallel()
	// In this copy train 153602, the RER D of the first journey, is cancelled.
	e := gareDuNordNow(t, "20261118T0557-siri-gare-du-nord-rer-d-edited-cancelled.json")

	out, err := e.svc.PlanJourney(t.Context(), plan("Gare du Nord", "Aéroport d'Orly"))
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Journeys) != 2 {
		t.Fatalf("journeys %+v, want two", out.Journeys)
	}
	if out.Journeys[0].Legs[0].Line != "4" || out.Journeys[1].Legs[0].Line != "D" {
		t.Errorf("first rides %s then %s, want 4 then the cancelled D",
			out.Journeys[0].Legs[0].Line, out.Journeys[1].Legs[0].Line)
	}
	if rt := out.Journeys[1].FirstLegRealtime; rt == nil || rt.Match != "trip" || rt.Status != "cancelled" {
		t.Errorf("RER D first_leg_realtime = %+v, want train 153602 cancelled", rt)
	}
}

func TestJourneyDisruptionsSortLiftsFromWorks(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	// The works as PRIM published them on the RER B line, untagged.
	works := `{"id": "works", "status": "future",
		"severity": {"name": "perturbée", "effect": "SIGNIFICANT_DELAYS"},
		"application_periods": [{"begin": "20261121T030000", "end": "20261122T030000"}],
		"messages": [{"text": "RER B : Aulnay-sous-Bois <-> Mitry - Claye 21-22/11",
			"channel": {"content_type": "text/plain", "types": ["title"]}}],
		"impacted_objects": [{"pt_object": {"id": "line:IDFM:C01743", "embedded_type": "line"}}]}`
	journeys := journeysJSON(t, []handJourney{{kind: "best", transfers: 0, walking: 0, durationS: 1800, rides: []ride{
		{
			from:        gareDuNord,
			to:          massy,
			dep:         "20261121T083000",
			code:        "B",
			trip:        "",
			disruptions: []string{"works", "lift"},
		},
	}}}, `[`+works+`,`+liftOutage("lift", gareDuNord)+`]`)
	e.fake.handle(navitia+"journeys", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, journeys) })

	out, err := e.svc.PlanJourney(t.Context(), plan("48.8809,2.3556", "48.7250,2.2590"))
	if err != nil {
		t.Fatal(err)
	}

	d := out.Journeys[0].Disruptions
	if len(d.Equipment) != 1 || d.Equipment[0].Stop != gareDuNord || d.Equipment[0].Title != "Panne d'un ascenseur" {
		t.Errorf("equipment %+v, want the lift at Gare du Nord", d.Equipment)
	}
	if len(d.Service) != 1 || d.Service[0].Title != "RER B : Aulnay-sous-Bois <-> Mitry - Claye 21-22/11" ||
		len(d.Service[0].Lines) != 1 || d.Service[0].Lines[0] != "B" {
		t.Errorf("service %+v, want the works on line B", d.Service)
	}
}

func TestStepFreeNeverPutsALongWalkFirst(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	journeys := journeysJSON(t, []handJourney{
		{kind: "best", transfers: 1, walking: 300, durationS: 2400, rides: []ride{
			{from: gareDuNord, to: chateletLH, dep: "20261119T083000", code: "B", trip: "", disruptions: nil},
			{from: chateletLH, to: orly, dep: "20261119T084000", code: "14", trip: "", disruptions: nil},
		}},
		// PRIM's walk-only alternative: no transfer, no lift, three hours.
		{kind: "non_pt_walk", transfers: 0, walking: 10800, durationS: 10800, rides: nil},
	}, `[]`)
	e.fake.handle(navitia+"journeys", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, journeys) })
	in := plan("48.8809,2.3556", "48.7289,2.3610")
	in.Profile = "step_free"

	out, err := e.svc.PlanJourney(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Journeys) == 0 || out.Journeys[0].Kind != "best" {
		t.Errorf("journeys %+v, want the public transport journey first", out.Journeys)
	}
}

func TestLongWalkIsNotListedBesideTransit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	e.fake.places(t)
	// PRIM answers Châtelet - Les Halles → La Défense with RER A and a
	// three-hour walk.
	e.fake.file(t, navitia+"journeys", "20261118T0521-journey-chatelet-la-defense.json")

	out, err := e.svc.PlanJourney(t.Context(), plan("48.8617,2.3470", "La Défense"))
	if err != nil {
		t.Fatal(err)
	}

	for _, j := range out.Journeys {
		if len(j.Legs) == 0 && j.DurationMin > 30 {
			t.Errorf("journey %s walks %d minutes", j.Kind, j.DurationMin)
		}
	}
	if len(out.Journeys) != 1 || out.Journeys[0].Legs[0].Line != "A" {
		t.Errorf("journeys %+v, want RER A alone", out.Journeys)
	}
}

func TestEachBrokenLiftIsListedOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	e.fake.places(t)
	// PRIM republishes the same lift fault at La Défense dozens of times, each
	// with its own end date.
	e.fake.file(t, navitia+"journeys", "20261118T0521-journey-chatelet-la-defense.json")

	out, err := e.svc.PlanJourney(t.Context(), plan("48.8617,2.3470", "La Défense"))
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	equipment := out.Journeys[0].Disruptions.Equipment
	for _, d := range equipment {
		key := d.Stop + "|" + d.Title + "|" + d.Text
		if seen[key] {
			t.Errorf("lift listed twice: %s: %s", d.Stop, d.Text)
		}
		seen[key] = true
	}
	if len(equipment) == 0 {
		t.Error("no broken lift listed")
	}
}

func TestFailedRealtimeIsNotAskedAgainInTheSameCall(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	ride := []ride{{from: gareDuNord, to: chateletLH, dep: "20261118T060000", code: "B", trip: "", disruptions: nil}}
	journeys := journeysJSON(t, []handJourney{
		{kind: "best", transfers: 0, walking: 0, durationS: 600, rides: ride},
		{kind: "comfort", transfers: 0, walking: 0, durationS: 700, rides: ride},
		{kind: "rapid", transfers: 0, walking: 0, durationS: 800, rides: ride},
	}, `[]`)
	e.fake.handle(navitia+"journeys", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, journeys) })
	e.fake.status(siri, http.StatusServiceUnavailable)

	_, err := e.svc.PlanJourney(t.Context(), plan("48.8809,2.3556", "48.8617,2.3470"))
	if err != nil {
		t.Fatal(err)
	}

	// The three first rides leave Gare du Nord on RER B: one request, no retry.
	if n := len(e.fake.requestsTo(siri)); n != 1 {
		t.Errorf("sent %d SIRI requests, want 1", n)
	}
}
