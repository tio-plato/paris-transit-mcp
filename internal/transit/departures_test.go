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

// saintLazareEnv serves Gare Saint-Lazare: its lines, the J line's SIRI
// recorded at 05:21, the N151 night bus's empty SIRI and its timetable.
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

	// Saint-Lazare is the J line's terminus: the trains towards it only arrive.
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
	// SIRI gives the J line no direction name: the direction names the termini
	// of the departures listed, three to Ermont - Eaubonne and one to
	// Boissy-l'Aillerie, not the five other termini later in the group.
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
	// The rail lines of Saint-Lazare: J, L, two TER Normandie lines, metro 3, 12, 13 and 14.
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

func TestDeparturesStopAtTheQuota(t *testing.T) {
	t.Parallel()
	e := saintLazareEnv(t)
	e.fake.status(siri, http.StatusTooManyRequests)

	_, err := e.svc.NextDepartures(t.Context(), departures("Gare Saint-Lazare", "J", "L", "N151"))

	if err == nil || transit.AsToolError(err).Code != transit.CodeQuota {
		t.Fatalf("error = %v, want quota", err)
	}
	if n := len(e.fake.requestsTo(siri)); n != 1 {
		t.Errorf("sent %d SIRI requests after a 429, want 1", n)
	}
	if n := len(e.fake.requestsTo(navitia + "stop_areas/" + saintLazare + "/departures")); n != 0 {
		t.Errorf("sent %d timetable requests after a 429", n)
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
