package transit_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/tio-plato/paris-transit-mcp/internal/transit"
)

// linesEnv answers /pt_objects with the recorded line searches and every
// line report with no disruption.
func linesEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, at(t, "20261118T0523"))
	e.fake.byQuery(t, navitia+"pt_objects", "q", map[string]string{
		"14": "20261118T0522-pt-objects-14.json",
		"6":  "20261118T0523-pt-objects-6.json",
		"b":  "20261118T0523-pt-objects-b.json",
		// The search for B also returned metro 3B and 7B.
		"3b": "20261118T0523-pt-objects-b.json",
		"7b": "20261118T0523-pt-objects-b.json",
	})
	e.fake.handle(navitia+"line_reports/*", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"line_reports": [], "disruptions": []}`)
	})
	return e
}

func status(lines ...string) transit.StatusInput {
	return transit.StatusInput{Lines: lines, Days: 0}
}

func TestLineNamesResolveToRailWhenOnlyOneIsRail(t *testing.T) {
	t.Parallel()
	e := linesEnv(t)

	out, err := e.svc.LineStatus(t.Context(), status("RER B", "14", "B"))
	if err != nil {
		t.Fatal(err)
	}

	// RER B is not RER Bus B, the replacement bus; bare 14 is metro 14, not
	// Vallée Sud Grand Paris bus 14; bare B is RER B.
	want := []string{"line:IDFM:C01743", "line:IDFM:C01384", "line:IDFM:C01743"}
	if len(out.Lines) != len(want) {
		t.Fatalf("lines %+v, want %d", out.Lines, len(want))
	}
	for i, l := range out.Lines {
		if l.Error != nil || l.LineID != want[i] {
			t.Errorf("line %d = %+v, want %s", i, l, want[i])
		}
	}
	reqs := e.fake.requestsTo(navitia + "line_reports/lines/line:IDFM:C01384/line_reports")
	if len(reqs) != 1 {
		t.Errorf("sent %d line report requests for metro 14, want 1", len(reqs))
	}
}

func TestAmbiguousLineNameListsCandidates(t *testing.T) {
	t.Parallel()
	e := linesEnv(t)

	out, err := e.svc.LineStatus(t.Context(), status("Bus 6", "14"))
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Lines) != 2 {
		t.Fatalf("lines %+v, want two", out.Lines)
	}
	bus := out.Lines[0]
	if bus.Error == nil || bus.Error.Code != transit.CodeAmbiguousLine {
		t.Fatalf("Bus 6: %+v, want ambiguous_line", bus)
	}
	// RATP and Vallée Sud Grand Paris both run a bus 6.
	ids := map[string]bool{}
	for _, c := range bus.Error.Candidates {
		ids[c.LineID] = true
	}
	if len(ids) != 2 || !ids["line:IDFM:C02268"] || !ids["line:IDFM:C00343"] {
		t.Errorf("candidates %+v, want the two bus 6 lines", bus.Error.Candidates)
	}
	if out.Lines[1].Error != nil || out.Lines[1].LineID != "line:IDFM:C01384" {
		t.Errorf("14: %+v, want metro 14 despite the ambiguous bus", out.Lines[1])
	}
}

func TestDisruptionTitleKeepsItsArrowAndTextLosesItsHTML(t *testing.T) {
	t.Parallel()
	e := linesEnv(t)
	// As PRIM writes them: a plain text title with <->, an HTML web message.
	e.fake.handle(navitia+"line_reports/*", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"line_reports": [], "disruptions": [{"id": "works", "status": "future",
			"severity": {"name": "perturbée", "effect": "SIGNIFICANT_DELAYS"},
			"application_periods": [{"begin": "20261121T030000", "end": "20261122T030000"}],
			"messages": [
				{"text": "RER B : Aulnay-sous-Bois <-> Mitry - Claye 21-22/11",
				 "channel": {"content_type": "text/plain", "types": ["title"]}},
				{"text": "<p>P&#233;riode : samedi.<br><br>Bus de remplacement</p>",
				 "channel": {"content_type": "text/html", "types": ["web"]}}]}]}`)
	})

	out, err := e.svc.LineStatus(t.Context(), status("RER B"))
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Lines) != 1 || len(out.Lines[0].Upcoming) != 1 {
		t.Fatalf("lines %+v, want one upcoming disruption", out.Lines)
	}
	r := out.Lines[0].Upcoming[0]
	if r.Title != "RER B : Aulnay-sous-Bois <-> Mitry - Claye 21-22/11" {
		t.Errorf("title %q, want it as PRIM wrote it", r.Title)
	}
	if r.Text != "Période : samedi.\nBus de remplacement" {
		t.Errorf("text %q, want the HTML as plain text", r.Text)
	}
}

func TestLineReportsNotFoundIsAnErrorNotAllClear(t *testing.T) {
	t.Parallel()
	e := linesEnv(t)
	// A line with no disruption answers 200: a 404 for a line just resolved
	// is a failure, and with every line failing, the call fails.
	e.fake.status(navitia+"line_reports/*", http.StatusNotFound)

	out, err := e.svc.LineStatus(t.Context(), status("RER B"))

	if err == nil || transit.AsToolError(err).Code != transit.CodeUpstream {
		t.Errorf("result %+v, error %v, want upstream rather than no disruption or not_found", out, err)
	}
}

func TestMetroBisLinesResolve(t *testing.T) {
	t.Parallel()
	e := linesEnv(t)

	out, err := e.svc.LineStatus(t.Context(), status("M3bis", "Métro 3bis", "M3B", "7bis"))
	if err != nil {
		t.Fatal(err)
	}

	// PRIM codes metro 3bis 3B and 7bis 7B.
	want := []string{"line:IDFM:C01386", "line:IDFM:C01386", "line:IDFM:C01386", "line:IDFM:C01387"}
	if len(out.Lines) != len(want) {
		t.Fatalf("lines %+v, want %d", out.Lines, len(want))
	}
	for i, l := range out.Lines {
		if l.Error != nil || l.LineID != want[i] {
			t.Errorf("line %d = %+v, want %s", i, l, want[i])
		}
	}
}

// rerDReports serves RER D's line reports recorded at 10:13 on a strike day,
// while the Corbeil-Essonnes <> Malesherbes works were in force.
func rerDReports(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, at(t, "20261118T1013"))
	e.fake.handle(navitia+"lines/line:IDFM:C01728", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"lines": [{"id": "line:IDFM:C01728", "code": "D", "name": "D",
			"commercial_mode": {"id": "commercial_mode:RapidTransit", "name": "RER"}, "network": {"name": "RER"}}]}`)
	})
	e.fake.file(t, navitia+"line_reports/lines/line:IDFM:C01728/line_reports", "20261118T1013-line-reports-rer-d.json")
	return e
}

func titles(reports []transit.Report) []string {
	var out []string
	for _, r := range reports {
		out = append(out, r.Title)
	}
	return out
}

func TestLineStatusLeavesBrokenLiftsOut(t *testing.T) {
	t.Parallel()
	e := rerDReports(t)

	out, err := e.svc.LineStatus(t.Context(), transit.StatusInput{Lines: []string{"line:IDFM:C01728"}, Days: 30})
	if err != nil {
		t.Fatal(err)
	}

	// The recording holds 130 lift faults, tagged Ascenseur, beside the works.
	l := out.Lines[0]
	for _, title := range append(titles(l.Active), titles(l.Upcoming)...) {
		if strings.Contains(title, "ascenseur") {
			t.Errorf("line_status lists the lift fault %q", title)
		}
	}
	if len(l.Active) == 0 || len(l.Upcoming) == 0 {
		t.Errorf(
			"active %v, upcoming %v: want the works and strike of the recording",
			titles(l.Active),
			titles(l.Upcoming),
		)
	}
}

func TestLineStatusSortsWorksInForceAndComing(t *testing.T) {
	t.Parallel()
	e := rerDReports(t)

	out, err := e.svc.LineStatus(t.Context(), transit.StatusInput{Lines: []string{"line:IDFM:C01728"}, Days: 7})
	if err != nil {
		t.Fatal(err)
	}

	l := out.Lines[0]
	const malesherbes = "RER D : Corbeil-Essonnes <> Malesherbes interrompu 16-20/11."
	var works *transit.Report
	for i := range l.Active {
		if l.Active[i].Title == malesherbes {
			works = &l.Active[i]
		}
	}
	if works == nil {
		t.Fatalf("active %v, want the Malesherbes works, in force 10:10-14:20", titles(l.Active))
	}
	// In force now, then the 19th and the 20th; the 16th and 17th are over.
	want := []transit.Period{
		{From: "2026-11-18T10:10:00+01:00", Until: "2026-11-18T14:20:00+01:00"},
		{From: "2026-11-19T10:10:00+01:00", Until: "2026-11-19T14:20:00+01:00"},
		{From: "2026-11-20T10:10:00+01:00", Until: "2026-11-20T14:20:00+01:00"},
	}
	if diff := cmp.Diff(want, works.Periods); diff != "" {
		t.Errorf("Malesherbes periods (-want +got):\n%s", diff)
	}
	upcoming := titles(l.Upcoming)
	for _, w := range []string{
		"RER D : Châtelet Les H. <-> Villeneuve St-G le 21/11, dès 23h20", // in 3 days
		"RER D - Stade de France - Match Rugby 21/11.",
	} {
		if !slices.Contains(upcoming, w) {
			t.Errorf("upcoming %v, want %q", upcoming, w)
		}
	}
	for _, w := range []string{
		malesherbes, // in force: active only
		"RER D : Stade de France <> Creil interruption 28-29/11.", // in 10 days
	} {
		if slices.Contains(upcoming, w) {
			t.Errorf("upcoming %v holds %q", upcoming, w)
		}
	}
	for _, r := range append(slices.Clone(l.Active), l.Upcoming...) {
		if len(r.Periods) > 5 {
			t.Errorf("%q lists %d periods, want at most 5", r.Title, len(r.Periods))
		}
	}
}

func TestLineStatusDaysBoundTheUpcomingWorks(t *testing.T) {
	t.Parallel()
	e := rerDReports(t)

	out, err := e.svc.LineStatus(t.Context(), transit.StatusInput{Lines: []string{"line:IDFM:C01728"}, Days: 3})
	if err != nil {
		t.Fatal(err)
	}

	// The match on the 21st at 16:00 is 3 days and 6 hours away.
	if upcoming := titles(
		out.Lines[0].Upcoming,
	); slices.Contains(
		upcoming,
		"RER D - Stade de France - Match Rugby 21/11.",
	) {
		t.Errorf("upcoming %v holds the match, beyond 3 days", upcoming)
	}
}
