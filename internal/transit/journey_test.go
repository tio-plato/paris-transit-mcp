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
