package transit_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/tio-plato/paris-transit-mcp/internal/transit"
)

const (
	lineJ    = "line:IDFM:C01739"
	lineL    = "line:IDFM:C01740"
	lineN151 = "line:IDFM:C01635"
)

// saintLazareEnv: J has live data, the N151 night bus only a timetable.
func saintLazareEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, at(t, "20261118T0521"))
	e.fake.places(t)
	e.fake.file(t, navitia+"stop_areas/"+saintLazare+"/lines", "20261118T0525-lines-saint-lazare.json")
	e.fake.byQuery(t, siri, "LineRef", map[string]string{
		"STIF:Line::C01739:": "20261118T0521-siri-saint-lazare-j.json",
		"STIF:Line::C01635:": "20261118T0525-siri-saint-lazare-n151.json",
	})
	e.fake.file(t, navitia+"stop_areas/"+saintLazare+"/departures", "20261118T0525-departures-saint-lazare-n151.json")
	return e
}

func departures(stop string, lines ...string) transit.DeparturesInput {
	return transit.DeparturesInput{Stop: stop, Lines: lines, Limit: 0}
}

func TestDeparturesAskSIRIForTheLine(t *testing.T) {
	t.Parallel()
	e := saintLazareEnv(t)

	out, err := e.svc.NextDepartures(t.Context(), departures("Gare Saint-Lazare", "J"))
	if err != nil {
		t.Fatal(err)
	}

	reqs := e.fake.requestsTo(siri)
	if len(reqs) != 1 || reqs[0].Query.Get("LineRef") != "STIF:Line::C01739:" ||
		reqs[0].Query.Get("MonitoringRef") != "STIF:StopArea:SP:71370:" {
		t.Errorf("SIRI requests %+v, want one for Saint-Lazare and the J line", reqs)
	}
	if len(out.Lines) == 0 || out.Lines[0].LineID != lineJ || !out.Lines[0].Realtime || len(out.Lines[0].Next) == 0 {
		t.Fatalf("lines %+v, want live J departures", out.Lines)
	}
}

func TestDeparturesAtATerminusListOnlyTrainsThatLeave(t *testing.T) {
	t.Parallel()
	e := saintLazareEnv(t)
	in := departures("Gare Saint-Lazare", "Transilien J")
	in.Limit = 4

	out, err := e.svc.NextDepartures(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	// Saint-Lazare is the J terminus: trains towards it only arrive.
	if len(out.Lines) != 1 {
		t.Fatalf("lines %+v, want the one direction that leaves Saint-Lazare", out.Lines)
	}
	next := out.Lines[0].Next
	if len(next) != 4 {
		t.Fatalf("next %+v, want the limit of 4", next)
	}
	for _, d := range next {
		if d.Destination == "" || d.Destination == "Paris Saint-Lazare" {
			t.Errorf("departure %+v: want a destination away from Saint-Lazare", d)
		}
	}
	// The first J departure, train 137505 to Ermont - Eaubonne, is at 04:31 UTC.
	if next[0].Time != "2026-11-18T05:31:00+01:00" || next[0].Destination != "Ermont - Eaubonne" {
		t.Errorf("first departure %+v, want Ermont - Eaubonne at 05:31 Paris time", next[0])
	}
	// No DirectionName from SIRI: only the termini shown, not all seven.
	if got := out.Lines[0].Direction; got != "Ermont - Eaubonne / Boissy-l'Aillerie" {
		t.Errorf("direction %q, want Ermont - Eaubonne / Boissy-l'Aillerie", got)
	}
}

func TestDeparturesWithoutLinesQueryTwelveRailFirst(t *testing.T) {
	t.Parallel()
	e := saintLazareEnv(t)
	e.fake.file(t, siri, "20261118T0521-siri-saint-lazare-j.json")

	out, err := e.svc.NextDepartures(t.Context(), departures("Gare Saint-Lazare"))
	if err != nil {
		t.Fatal(err)
	}

	var asked []string
	for _, r := range e.fake.requestsTo(siri) {
		asked = append(asked, r.Query.Get("LineRef"))
	}
	if len(asked) != transit.MaxStopLines {
		t.Errorf("sent %d SIRI requests, want %d", len(asked), transit.MaxStopLines)
	}
	// J, L, two TER lines, metro 3, 12, 13, 14.
	for _, rail := range []string{"C01739", "C01740", "C01748", "C02375", "C01373", "C01382", "C01383", "C01384"} {
		if !slices.Contains(asked, "STIF:Line::"+rail+":") {
			t.Errorf("no SIRI request for rail line %s", rail)
		}
	}
	// Saint-Lazare has 38 lines.
	if len(out.SkippedLines) != 38-transit.MaxStopLines {
		t.Errorf("skipped %d lines, want %d", len(out.SkippedLines), 38-transit.MaxStopLines)
	}
	for _, s := range out.SkippedLines {
		if s.Mode != "Bus" && s.Mode != "Noctilien" {
			t.Errorf("skipped %+v, a line that is not a bus", s)
		}
	}
}

func TestDeparturesNearAPlaceUseTheNearestStation(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0521"))
	e.fake.file(t, navitia+"coords/*", "20261118T0521-nearby-forum-des-halles.json")
	e.fake.handle(navitia+"stop_areas/*", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"lines": []}`)
	})

	out, err := e.svc.NextDepartures(t.Context(), departures(forum))
	if err != nil {
		t.Fatal(err)
	}

	if out.Stop == nil || out.Stop.Resolved.ID != chateletLH {
		t.Errorf("stop %+v, want Châtelet - Les Halles", out.Stop)
	}
}

func TestDeparturesFallBackToTheTimetable(t *testing.T) {
	t.Parallel()
	e := saintLazareEnv(t)

	out, err := e.svc.NextDepartures(t.Context(), departures("Gare Saint-Lazare", "N151"))
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Lines) != 1 || out.Lines[0].Realtime || len(out.Lines[0].Next) == 0 {
		t.Fatalf("lines %+v, want timetabled N151 departures", out.Lines)
	}
	// The first N151 of the timetable leaves at 02:00 the next night.
	if got := out.Lines[0].Next[0].Time; got != "2026-11-19T02:00:00+01:00" {
		t.Errorf("first departure at %s, want 2026-11-19T02:00:00+01:00", got)
	}
	reqs := e.fake.requestsTo(navitia + "stop_areas/" + saintLazare + "/departures")
	if len(reqs) != 1 || reqs[0].Query.Get("filter") != "line.id="+lineN151 ||
		reqs[0].Query.Get("data_freshness") != "base_schedule" {
		t.Errorf("timetable requests %+v, want base_schedule for N151", reqs)
	}
}

func TestDeparturesOfALineWhoseSIRIFailsComeFromTheTimetable(t *testing.T) {
	t.Parallel()
	e := saintLazareEnv(t)
	e.fake.handle(siri, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("LineRef") == "STIF:Line::C01635:" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, string(readFixture(t, "20261118T0521-siri-saint-lazare-j.json")))
	})

	out, err := e.svc.NextDepartures(t.Context(), departures("Gare Saint-Lazare", "N151", "J"))
	if err != nil {
		t.Fatal(err)
	}

	realtime := map[string]bool{}
	for _, l := range out.Lines {
		if l.Error != nil {
			t.Errorf("line %s: error %+v", l.LineID, l.Error)
		}
		realtime[l.LineID] = l.Realtime
	}
	if realtime[lineN151] || !realtime[lineJ] {
		t.Errorf("realtime by line %v, want N151 from the timetable and J live", realtime)
	}
}

func TestSIRIQuotaFallsBackToTheTimetableAndSaysSo(t *testing.T) {
	t.Parallel()
	e := saintLazareEnv(t)
	e.fake.status(siri, http.StatusTooManyRequests)

	out, err := e.svc.NextDepartures(t.Context(), departures("Gare Saint-Lazare", "J", "L", "N151"))
	if err != nil {
		t.Fatal(err)
	}

	if n := len(e.fake.requestsTo(siri)); n != 1 {
		t.Errorf("sent %d SIRI requests after a 429, want 1", n)
	}
	if out.RealtimeUnavailable == nil || out.RealtimeUnavailable.Code != transit.CodeQuota {
		t.Errorf("realtime_unavailable = %+v, want quota", out.RealtimeUnavailable)
	}
	for _, l := range out.Lines {
		if l.Realtime || l.Error != nil {
			t.Errorf("line %s: %+v, want timetable departures", l.LineID, l)
		}
	}
}

func TestNavitiaQuotaStillFailsTheCall(t *testing.T) {
	t.Parallel()
	e := saintLazareEnv(t)
	e.fake.status(navitia+"stop_areas/"+saintLazare+"/departures", http.StatusTooManyRequests)

	_, err := e.svc.NextDepartures(t.Context(), departures("Gare Saint-Lazare", "N151", "J"))

	if err == nil || transit.AsToolError(err).Code != transit.CodeQuota {
		t.Fatalf("error = %v, want quota", err)
	}
}

func TestDeparturesOfALineNotAtTheStopIsNotFound(t *testing.T) {
	t.Parallel()
	e := saintLazareEnv(t)

	out, err := e.svc.NextDepartures(t.Context(), departures("Gare Saint-Lazare", "RER B", "J"))
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Lines) < 2 || out.Lines[0].Error == nil || out.Lines[0].Error.Code != transit.CodeNotFound {
		t.Fatalf("lines %+v, want RER B not_found and J", out.Lines)
	}
	if out.Lines[1].LineID != lineJ {
		t.Errorf("second line %+v, want J", out.Lines[1])
	}
}

func TestDeparturesGroupByDirectionAndKeepCancelledTrains(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0557"))
	e.fake.places(t)
	e.fake.file(t, navitia+"stop_areas/"+gareDuNord+"/lines", "20261118T0557-lines-gare-du-nord.json")
	e.fake.file(t, siri, "20261118T0557-siri-gare-du-nord-rer-d.json")
	in := departures("Gare du Nord", "RER D")
	in.Limit = 10

	out, err := e.svc.NextDepartures(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	// RER D leaves Gare du Nord both ways: SIRI's Aller and Retour.
	if len(out.Lines) != 2 {
		t.Fatalf("lines %+v, want two directions", out.Lines)
	}
	cancelled := 0
	for _, l := range out.Lines {
		if l.LineID != "line:IDFM:C01728" || !l.Realtime || len(l.Next) == 0 || len(l.Next) > 10 {
			t.Errorf("direction %q: %+v, want 1 to 10 live RER D departures", l.Direction, l)
		}
		for _, d := range l.Next {
			if d.Destination == "" {
				t.Errorf("departure %+v has no destination", d)
			}
			if d.Status != nil && *d.Status == "cancelled" {
				cancelled++
			}
		}
	}
	// Train 126603 to Villiers-le-Bel, due at 06:13:30, is cancelled.
	if cancelled == 0 {
		t.Error("no cancelled departure, want train 126603 cancelled")
	}
}

func TestEmptyTimetableIsNoDepartureButABodiless404IsAFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status    int
		body      string
		wantError string
	}{
		// What Navitia returned on 2026-11-18.
		{http.StatusOK, `{"departures": [], "pagination": {"total_result": 0}}`, ""},
		{http.StatusNotFound, ``, transit.CodeUpstream},
		{http.StatusNotFound, `{"error": {"id": "no_departure_this_day", "message": "no departure"}}`, ""},
	} {
		e := saintLazareEnv(t)
		e.fake.handle(navitia+"stop_areas/"+saintLazare+"/departures", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, tc.status, tc.body)
		})

		out, err := e.svc.NextDepartures(t.Context(), departures("Gare Saint-Lazare", "N151"))
		if err != nil {
			t.Fatal(err)
		}

		if len(out.Lines) != 1 {
			t.Fatalf("%d %q: lines %+v, want one", tc.status, tc.body, out.Lines)
		}
		l := out.Lines[0]
		got := ""
		if l.Error != nil {
			got = l.Error.Code
		}
		if got != tc.wantError || l.Realtime || len(l.Next) != 0 {
			t.Errorf("%d %q: line %+v, want error %q and no departure", tc.status, tc.body, l, tc.wantError)
		}
	}
}

func TestALineNamedTwiceIsAskedOnce(t *testing.T) {
	t.Parallel()
	e := saintLazareEnv(t)

	out, err := e.svc.NextDepartures(t.Context(), departures("Gare Saint-Lazare", "J", "Transilien J", lineJ))
	if err != nil {
		t.Fatal(err)
	}

	if n := len(e.fake.requestsTo(siri)); n != 1 {
		t.Errorf("sent %d SIRI requests for one line, want 1", n)
	}
	if len(out.Lines) != 1 {
		t.Errorf("lines %+v, want the J line once", out.Lines)
	}
}
