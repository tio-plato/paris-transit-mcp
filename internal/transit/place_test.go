package transit_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tio-plato/paris-transit-mcp/internal/transit"
)

func TestCoordinatesGoToPRIMAsLongitudeLatitude(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0520"))
	e.fake.file(t, navitia+"journeys", "20261118T0520-journey-gare-du-nord-massy.json")

	_, err := e.svc.PlanJourney(t.Context(), transit.JourneyInput{
		Origin: "48.8584,2.3470", Destination: "48.7250,2.2590",
		Datetime: "", DatetimeRepresents: "", Profile: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	reqs := e.fake.requestsTo(navitia + "journeys")
	if len(reqs) == 0 {
		t.Fatal("no journeys request")
	}
	lon, lat, ok := strings.Cut(reqs[0].Query.Get("from"), ";")
	if !ok || parse(t, lon) != 2.347 || parse(t, lat) != 48.8584 {
		t.Errorf("from = %q, want longitude 2.347;latitude 48.8584", reqs[0].Query.Get("from"))
	}
}

func parse(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return f
}

func TestStationNameTakesTheFirstStation(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0520"))
	e.fake.places(t)
	e.fake.file(t, navitia+"journeys", "20261118T0520-journey-gare-du-nord-massy.json")

	out, err := e.svc.PlanJourney(t.Context(), transit.JourneyInput{
		Origin: "Victor Hugo", Destination: "48.7250,2.2590", Datetime: "", DatetimeRepresents: "", Profile: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if out.Error != nil {
		t.Fatalf("error %+v", out.Error)
	}
	if got := out.Origin.Resolved; got.ID != "stop_area:IDFM:71315" || got.Name != "Victor Hugo (Paris)" {
		t.Errorf("origin resolved to %+v, want Victor Hugo (Paris)", got)
	}
}

func TestHomonymousAddressIsAmbiguous(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0519"))
	e.fake.places(t)

	out, err := e.svc.PlanJourney(t.Context(), transit.JourneyInput{
		Origin: "12 rue Victor Hugo", Destination: "48.7250,2.2590", Datetime: "", DatetimeRepresents: "", Profile: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if out.Error == nil || out.Error.Code != transit.CodeAmbiguousPlace {
		t.Fatalf("error = %+v, want ambiguous_place", out.Error)
	}
	towns := map[string]bool{}
	for _, c := range out.Error.Candidates {
		if c.ID == "" || c.Town == "" {
			t.Errorf("candidate %+v lacks an id or a town", c)
		}
		towns[c.Town] = true
	}
	if !towns["Montreuil"] || !towns["Argenteuil"] || len(out.Error.Candidates) > 8 {
		t.Errorf("candidates %+v: want the towns of the first 8 results", out.Error.Candidates)
	}
	if n := len(e.fake.requestsTo(navitia + "journeys")); n != 0 {
		t.Errorf("sent %d journeys requests for an ambiguous place", n)
	}
}

func TestAddressWithItsTownIsNotAmbiguous(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0520"))
	e.fake.places(t)
	e.fake.file(t, navitia+"journeys", "20261118T0520-journey-gare-du-nord-massy.json")

	out, err := e.svc.PlanJourney(t.Context(), transit.JourneyInput{
		Origin: "12 rue Victor Hugo Montreuil", Destination: "48.7250,2.2590",
		Datetime: "", DatetimeRepresents: "", Profile: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if out.Error != nil {
		t.Fatalf("error %+v", out.Error)
	}
	if got := out.Origin.Resolved.Name; got != "12 Rue Victor Hugo (Montreuil)" {
		t.Errorf("origin resolved to %q, want the Montreuil address", got)
	}
}

func TestPlaceWithoutResultIsNotFound(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0520"))
	e.fake.handle(navitia+"places", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, `{}`) })

	out, err := e.svc.PlanJourney(t.Context(), transit.JourneyInput{
		Origin: "Nowhere at all", Destination: "48.7250,2.2590", Datetime: "", DatetimeRepresents: "", Profile: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if out.Error == nil || out.Error.Code != transit.CodeNotFound {
		t.Fatalf("error = %+v, want not_found", out.Error)
	}
}

func TestTimeWithoutOffsetIsParisTime(t *testing.T) {
	t.Parallel()
	e := newEnv(t, at(t, "20261118T0520"))
	e.fake.file(t, navitia+"journeys", "20261118T0520-journey-gare-du-nord-massy.json")

	out, err := e.svc.PlanJourney(t.Context(), transit.JourneyInput{
		Origin: "48.8584,2.3470", Destination: "48.7250,2.2590",
		Datetime: "2026-11-20T08:30", DatetimeRepresents: "arrival", Profile: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	link, err := url.Parse(out.Journeys[0].CitymapperURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := link.Query().Get("arrival_time"); got != "2026-11-20T08:30:00+01:00" {
		t.Errorf("arrival_time = %q, want 2026-11-20T08:30:00+01:00", got)
	}
}

func TestTimeMoreThanAnHourAgoIsRefused(t *testing.T) {
	t.Parallel()
	now := at(t, "20261118T0520")
	e := newEnv(t, now)
	e.fake.file(t, navitia+"journeys", "20261118T0520-journey-gare-du-nord-massy.json")

	for _, tc := range []struct {
		ago     time.Duration
		refused bool
	}{{2 * time.Hour, true}, {30 * time.Minute, false}} {
		_, err := e.svc.PlanJourney(t.Context(), transit.JourneyInput{
			Origin: "48.8584,2.3470", Destination: "48.7250,2.2590",
			Datetime: now().Add(-tc.ago).Format(time.RFC3339), DatetimeRepresents: "", Profile: "",
		})
		refused := err != nil && transit.AsToolError(err).Code == transit.CodeBadRequest
		if refused != tc.refused {
			t.Errorf("%v ago: error %v, want refused %v", tc.ago, err, tc.refused)
		}
	}
	if n := len(e.fake.requestsTo(navitia + "journeys")); n != 1 {
		t.Errorf("sent %d journeys requests, want 1 (for the time 30 minutes ago)", n)
	}
}
