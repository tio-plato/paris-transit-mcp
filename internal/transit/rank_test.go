package transit_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/tio-plato/paris-transit-mcp/internal/transit"
)

func destination(label, place string) transit.DestinationInput {
	return transit.DestinationInput{Label: label, Place: place, ArriveBy: ""}
}

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

func TestRankSortsByDurationThenTransfersThenWalking(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	journey := func(transfers, walkingS, durationS int) string {
		return journeysJSON(
			t,
			[]handJourney{
				{
					kind:      "best",
					transfers: transfers,
					walking:   walkingS,
					durationS: durationS,
					rides: []ride{
						{from: gareDuNord, to: orly, dep: "20261119T083000", code: "B", trip: "", disruptions: nil},
					},
				},
			},
			`[]`,
		)
	}
	bodies := map[string]string{
		"48.1,2.1": journey(0, 60, 2400),  // 40 minutes
		"48.2,2.2": journey(2, 60, 1800),  // 30 minutes, 2 transfers
		"48.3,2.3": journey(1, 60, 1800),  // 30 minutes, 1 transfer, 1 minute's walk
		"48.4,2.4": journey(1, 300, 1800), // the same with 5 minutes' walk
	}
	e.fake.handle(navitia+"journeys", func(w http.ResponseWriter, r *http.Request) {
		lon, lat, _ := strings.Cut(r.URL.Query().Get("to"), ";")
		writeJSON(w, http.StatusOK, bodies[lat+","+lon])
	})

	out, err := e.svc.RankDestinations(t.Context(), transit.RankInput{
		Origin: "48.8809,2.3556",
		Destinations: []transit.DestinationInput{
			destination("forty", "48.1,2.1"), destination("two transfers", "48.2,2.2"),
			destination("one transfer, long walk", "48.4,2.4"), destination("one transfer", "48.3,2.3"),
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
	want := []string{"one transfer", "one transfer, long walk", "two transfers", "forty"}
	if diff := cmp.Diff(want, labels); diff != "" {
		t.Errorf("ranked (-want +got):\n%s", diff)
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
		// Walk only, and both ends are stations.
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

func TestRankReplacesAWalkOnlyOriginOnceAndSaysSo(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	e.fake.places(t)
	e.fake.file(t, navitia+"coords/*", "20261118T0521-nearby-forum-des-halles.json")
	walk := readFixture(t, "20261118T0521-journey-forum-des-halles-la-defense.json")
	viaRERA := readFixture(t, "20261118T0521-journey-chatelet-la-defense.json")
	e.fake.handle(navitia+"journeys", func(w http.ResponseWriter, r *http.Request) {
		// PRIM finds only a walk from the point inside the Forum des Halles.
		if r.URL.Query().Get("from") == chateletLH {
			writeJSON(w, http.StatusOK, string(viaRERA))
			return
		}
		writeJSON(w, http.StatusOK, string(walk))
	})

	out, err := e.svc.RankDestinations(t.Context(), transit.RankInput{
		Origin: forum,
		Destinations: []transit.DestinationInput{
			destination("one", "La Défense"), destination("two", "La Défense"), destination("three", "La Défense"),
		},
		DepartAt: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if n := len(e.fake.requestsTo(navitia + "coords/")); n != 1 {
		t.Errorf("looked for a station near the origin %d times, want once", n)
	}
	o := out.Origin.Resolved
	if o.ID != chateletLH || o.SnappedFrom == nil || o.SnappedFrom.Name != forum {
		t.Errorf("origin %+v, want Châtelet - Les Halles, snapped from %s", o, forum)
	}
	if len(out.Ranked) != 3 {
		t.Fatalf("ranked %+v, failed %+v, want three", out.Ranked, out.Failed)
	}
	link, err := url.Parse(out.Ranked[2].CitymapperURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := link.Query().Get("startcoord"); got != "48.8619,2.347" {
		t.Errorf("startcoord %q, want the user's 48.8619,2.347", got)
	}
}

func TestRankKeepsTheOriginWhenADestinationCausedTheSnap(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	e.fake.file(t, navitia+"coords/*", "20261118T0521-nearby-forum-des-halles.json")
	walk := readFixture(t, "20261118T0521-journey-forum-des-halles-la-defense.json")
	viaRERA := readFixture(t, "20261118T0521-journey-chatelet-la-defense.json")
	e.fake.handle(navitia+"journeys", func(w http.ResponseWriter, r *http.Request) {
		// The Forum point and its station only get a walk.
		to := r.URL.Query().Get("to")
		if to == forumCoord || to == chateletLH {
			writeJSON(w, http.StatusOK, string(walk))
			return
		}
		writeJSON(w, http.StatusOK, string(viaRERA))
	})
	origin := "48.8566,2.3522"

	out, err := e.svc.RankDestinations(t.Context(), transit.RankInput{
		Origin: origin,
		Destinations: []transit.DestinationInput{
			destination("a", "48.8920,2.2380"), destination("forum", forum), destination("c", "48.8700,2.3000"),
		},
		DepartAt: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	reqs := e.fake.requestsTo(navitia + "journeys")
	last := reqs[len(reqs)-1]
	if last.Query.Get("from") != "2.3522;48.8566" {
		t.Errorf("c planned from %q, want the asked origin: the forum destination caused the snap",
			last.Query.Get("from"))
	}
	if out.Origin.Resolved.SnappedFrom != nil {
		t.Errorf("origin %+v, want it as asked", out.Origin.Resolved)
	}
}
