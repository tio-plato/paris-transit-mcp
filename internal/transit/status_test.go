package transit_test

import (
	"net/http"
	"testing"

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
	// A gateway 404 has no Navitia body; a line with no disruption answers 200.
	e.fake.status(navitia+"line_reports/*", http.StatusNotFound)

	out, err := e.svc.LineStatus(t.Context(), status("RER B"))
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Lines) != 1 || out.Lines[0].Error == nil {
		t.Errorf("lines %+v, want an error rather than no disruption", out.Lines)
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
