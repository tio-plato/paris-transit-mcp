package transit_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tio-plato/paris-transit-mcp/internal/transit"
)

func destination(label, place string) transit.DestinationInput {
	return transit.DestinationInput{Label: label, Place: place, ArriveBy: ""}
}

// rankEnv answers each journeys request by its destination.
func rankEnv(t *testing.T, journeys map[string]string) *env {
	t.Helper()
	e := newEnv(t, at(t, "20261118T0521"))
	e.fake.places(t)
	bodies := map[string]string{}
	for to, name := range journeys {
		bodies[to] = string(readFixture(t, name))
	}
	e.fake.handle(navitia+"journeys", func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Query().Get("to")]
		if !ok {
			writeJSON(w, http.StatusNotFound, `{"error": {"id": "no_solution", "message": "no solution"}}`)
			return
		}
		writeJSON(w, http.StatusOK, body)
	})
	return e
}

func TestRankSortsByDurationThenTransfers(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	ride := func(to string) []ride {
		return []ride{{from: gareDuNord, to: to, dep: "20261119T083000", code: "B", trip: "", disruptions: nil}}
	}
	bodies := map[string]string{
		// 40 minutes; 30 minutes with 2 transfers; 30 minutes with 1 transfer.
		"48.1,2.1": journeysJSON(
			t,
			[]handJourney{{kind: "best", transfers: 0, walking: 60, durationS: 2400, rides: ride(orly)}},
			`[]`,
		),
		"48.2,2.2": journeysJSON(
			t,
			[]handJourney{{kind: "best", transfers: 2, walking: 60, durationS: 1800, rides: ride(massy)}},
			`[]`,
		),
		"48.3,2.3": journeysJSON(
			t,
			[]handJourney{{kind: "best", transfers: 1, walking: 60, durationS: 1800, rides: ride(laDefense)}},
			`[]`,
		),
	}
	e.fake.handle(navitia+"journeys", func(w http.ResponseWriter, r *http.Request) {
		to := r.URL.Query().Get("to")
		lon, lat, _ := strings.Cut(to, ";")
		writeJSON(w, http.StatusOK, bodies[lat+","+lon])
	})

	out, err := e.svc.RankDestinations(t.Context(), transit.RankInput{
		Origin: "48.8809,2.3556",
		Destinations: []transit.DestinationInput{
			destination(
				"forty",
				"48.1,2.1",
			),
			destination("two transfers", "48.2,2.2"),
			destination("one transfer", "48.3,2.3"),
		},
		DepartAt: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	var labels []string
	for _, r := range out.Ranked {
		labels = append(labels, r.Label)
	}
	if len(labels) != 3 || labels[0] != "one transfer" || labels[1] != "two transfers" || labels[2] != "forty" {
		t.Errorf("ranked %v, want one transfer, two transfers, forty", labels)
	}
	for _, r := range e.fake.requestsTo(navitia + "journeys") {
		if r.Query.Get("max_nb_journeys") != "1" {
			t.Errorf("journeys request %v asks for more than one journey", r.Query)
		}
	}
}

func TestRankPutsAFailedDestinationAside(t *testing.T) {
	t.Parallel()
	e := rankEnv(t, map[string]string{
		massy: "20261118T0520-journey-gare-du-nord-massy.json",
		// Walk only; both ends are stations, so there is nothing to snap.
		laDefense: "20261118T0521-journey-forum-des-halles-la-defense.json",
	})

	out, err := e.svc.RankDestinations(t.Context(), transit.RankInput{
		Origin: "Gare du Nord",
		Destinations: []transit.DestinationInput{
			destination("walk", "La Défense"), destination("homonym", "12 rue Victor Hugo"),
			destination("massy", "Massy-Palaiseau"),
		},
		DepartAt: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Ranked) != 1 || out.Ranked[0].Label != "massy" {
		t.Errorf("ranked %+v, want massy only", out.Ranked)
	}
	codes := map[string]string{}
	for _, f := range out.Failed {
		codes[f.Label] = f.Error.Code
		if f.Label == "homonym" && len(f.Error.Candidates) < 2 {
			t.Errorf("homonym candidates %+v, want several towns", f.Error.Candidates)
		}
	}
	if codes["walk"] != transit.CodeNoTransitRoute || codes["homonym"] != transit.CodeAmbiguousPlace {
		t.Errorf("failed %+v, want walk no_transit_route and homonym ambiguous_place", out.Failed)
	}
}

func TestRankStopsAtTheQuota(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	massyJourney := readFixture(t, "20261118T0520-journey-gare-du-nord-massy.json")
	calls := 0
	e.fake.handle(navitia+"journeys", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeJSON(w, http.StatusOK, string(massyJourney))
	})

	_, err := e.svc.RankDestinations(t.Context(), transit.RankInput{
		Origin: "48.8809,2.3556",
		Destinations: []transit.DestinationInput{
			destination("one", "48.1,2.1"), destination("two", "48.2,2.2"), destination("three", "48.3,2.3"),
		},
		DepartAt: "",
	})

	if err == nil || transit.AsToolError(err).Code != transit.CodeQuota {
		t.Fatalf("error = %v, want quota", err)
	}
	if n := len(e.fake.requestsTo(navitia + "journeys")); n != 2 {
		t.Errorf("sent %d journeys requests, want 2", n)
	}
}

func TestRankArrivesByEachDestinationsTime(t *testing.T) {
	t.Parallel()
	e := rankEnv(t, map[string]string{massy: "20261118T0520-journey-gare-du-nord-massy.json"})

	_, err := e.svc.RankDestinations(t.Context(), transit.RankInput{
		Origin: "Gare du Nord",
		Destinations: []transit.DestinationInput{
			{Label: "massy", Place: "Massy-Palaiseau", ArriveBy: "2026-11-19T09:00"},
		},
		DepartAt: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	reqs := e.fake.requestsTo(navitia + "journeys")
	if len(reqs) != 1 || reqs[0].Query.Get("datetime_represents") != "arrival" ||
		reqs[0].Query.Get("datetime") != "20261119T090000" {
		t.Errorf("journeys requests %+v, want arrival at 20261119T090000", reqs)
	}
}
