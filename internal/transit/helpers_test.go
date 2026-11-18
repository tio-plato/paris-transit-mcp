package transit_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tio-plato/paris-transit-mcp/internal/prim"
	"github.com/tio-plato/paris-transit-mcp/internal/transit"
)

const (
	navitia = "/v2/navitia/"
	siri    = "/stop-monitoring"

	gareDuNord  = "stop_area:IDFM:71410"
	saintLazare = "stop_area:IDFM:71370"
	chateletLH  = "stop_area:IDFM:474151"
	laDefense   = "stop_area:IDFM:71517"
	orly        = "stop_area:IDFM:63284"
	massy       = "stop_area:IDFM:63244"
	// forum is a point inside the Forum des Halles, where PRIM finds only a walk.
	forum      = "48.8619,2.3470"
	forumCoord = "2.347;48.8619"
)

// at returns the Paris time of a fixture's name prefix, such as 20261118T0521.
func at(t *testing.T, stamp string) func() time.Time {
	t.Helper()
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.ParseInLocation("20060102T1504", stamp, paris)
	if err != nil {
		t.Fatal(err)
	}
	return func() time.Time { return now }
}

// request is what the fake server received.
type request struct {
	Path  string
	Query url.Values
}

// fake stands in for PRIM. Handlers are looked up by path, then by path
// prefix; anything else is a 404.
type fake struct {
	mu       sync.Mutex
	requests []request
	handlers map[string]http.HandlerFunc
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, request{Path: r.URL.Path, Query: r.URL.Query()})
	h := f.handlers[r.URL.Path]
	if h == nil {
		longest := ""
		for prefix, ph := range f.handlers {
			p, ok := strings.CutSuffix(prefix, "*")
			if ok && strings.HasPrefix(r.URL.Path, p) && len(p) > len(longest) {
				h, longest = ph, p
			}
		}
	}
	f.mu.Unlock()
	if h == nil {
		writeJSON(w, http.StatusNotFound, `{"error": {"id": "unknown_object", "message": "not in the fake"}}`)
		return
	}
	h(w, r)
}

// handle sets how the fake answers path; a path ending in * is a prefix.
func (f *fake) handle(path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[path] = h
}

// file answers path with a fixture.
func (f *fake) file(t *testing.T, path, name string) {
	t.Helper()
	data := readFixture(t, name)
	f.handle(path, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, string(data)) })
}

// sequence answers path with each fixture in turn, then the last one again.
func (f *fake) sequence(t *testing.T, path string, names ...string) {
	t.Helper()
	bodies := make([][]byte, 0, len(names))
	for _, n := range names {
		bodies = append(bodies, readFixture(t, n))
	}
	var mu sync.Mutex
	i := 0
	f.handle(path, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		body := bodies[min(i, len(bodies)-1)]
		i++
		mu.Unlock()
		writeJSON(w, http.StatusOK, string(body))
	})
}

// status answers path with an HTTP status and no body.
func (f *fake) status(path string, code int) {
	f.handle(path, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
}

// requestsTo are the requests whose path starts with prefix.
func (f *fake) requestsTo(prefix string) []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.DeleteFunc(
		slices.Clone(f.requests),
		func(r request) bool { return !strings.HasPrefix(r.Path, prefix) },
	)
}

type env struct {
	svc  *transit.Service
	fake *fake
}

// newEnv starts a fake PRIM with no handlers and a service whose clock is now.
func newEnv(t *testing.T, now func() time.Time) *env {
	t.Helper()
	f := &fake{mu: sync.Mutex{}, requests: nil, handlers: map[string]http.HandlerFunc{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	client := prim.New(prim.Config{BaseURL: srv.URL, Token: "test-token", Timeout: 0, Rate: 1000})
	svc, err := transit.New(client, now)
	if err != nil {
		t.Fatal(err)
	}
	return &env{svc: svc, fake: f}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// byQuery answers path with the fixture named for the value of a query
// parameter; another value gets a 404.
func (f *fake) byQuery(t *testing.T, path, param string, fixtures map[string]string) {
	t.Helper()
	bodies := map[string][]byte{}
	for value, name := range fixtures {
		bodies[value] = readFixture(t, name)
	}
	f.handle(path, func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Query().Get(param)]
		if !ok {
			writeJSON(w, http.StatusNotFound, `{"error": {"id": "unknown_object", "message": "not in the fake"}}`)
			return
		}
		writeJSON(w, http.StatusOK, string(body))
	})
}

// places answers /places?q= with the recorded searches.
func (f *fake) places(t *testing.T) {
	t.Helper()
	f.byQuery(t, navitia+"places", "q", map[string]string{
		"12 rue Victor Hugo":           "20261118T0519-places-12-rue-victor-hugo.json",
		"12 rue Victor Hugo Montreuil": "20261118T0519-places-12-rue-victor-hugo.json",
		"Victor Hugo":                  "20261118T0519-places-victor-hugo.json",
		"La Défense":                   "20261118T0519-places-la-defense.json",
		"Gare du Nord":                 "20261118T0520-places-gare-du-nord.json",
		"Aéroport d'Orly":              "20261118T0520-places-orly.json",
		"Massy-Palaiseau":              "20261118T0520-places-massy-palaiseau.json",
		"Gare Saint-Lazare":            "20261118T0520-places-gare-saint-lazare.json",
	})
}

// ride is a public transport section of a hand-written journey.
type ride struct {
	from, to    string // stop area ids
	dep         string // Navitia time
	code        string
	trip        string
	disruptions []string
}

// handJourney is a hand-written Navitia journey, for rules that the recorded
// responses do not exercise.
type handJourney struct {
	kind                          string
	transfers, walking, durationS int
	rides                         []ride
}

// journeysJSON writes a Navitia journeys response; disruptions is the JSON
// array of the top-level disruptions.
func journeysJSON(t *testing.T, journeys []handJourney, disruptions string) string {
	t.Helper()
	type obj = map[string]any
	stop := func(id string) obj {
		return obj{"id": id, "name": id, "embedded_type": "stop_point",
			"stop_point": obj{"id": id, "name": id, "stop_area": obj{"id": id, "name": id + " (Paris)"}}}
	}
	var js []obj
	for _, j := range journeys {
		var sections []obj
		for _, r := range j.rides {
			var links []obj
			for _, d := range r.disruptions {
				links = append(links, obj{"type": "disruption", "rel": "disruptions", "id": d})
			}
			sections = append(sections, obj{
				"type": "public_transport", "from": stop(r.from), "to": stop(r.to),
				"departure_date_time": r.dep, "base_departure_date_time": r.dep, "arrival_date_time": r.dep,
				"display_informations": obj{"code": r.code, "commercial_mode": "RER", "direction": "Terminus",
					"trip_short_name": r.trip, "links": links},
				"links": []obj{{"type": "line", "id": "line:IDFM:C01743"},
					{"type": "commercial_mode", "id": "commercial_mode:RapidTransit"}},
				"stop_date_times": []obj{{}, {}},
			})
		}
		departure := "20261118T060000"
		if len(j.rides) > 0 {
			departure = j.rides[0].dep
		}
		js = append(js, obj{"type": j.kind, "duration": j.durationS, "nb_transfers": j.transfers,
			"departure_date_time": departure, "arrival_date_time": "20261118T070000",
			"durations": obj{"walking": j.walking}, "sections": sections})
	}
	var ds []any
	err := json.Unmarshal([]byte(disruptions), &ds)
	if err != nil {
		t.Fatalf("disruptions: %v", err)
	}
	data, err := json.Marshal(obj{"journeys": js, "disruptions": ds})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// liftOutage is an equipment disruption at a stop area, as PRIM publishes it.
func liftOutage(id, stopArea string) string {
	return `{"id": "` + id + `", "status": "active", "tags": ["Ascenseur"],
		"severity": {"name": "perturbée", "effect": "SIGNIFICANT_DELAYS"},
		"application_periods": [{"begin": "20260101T000000", "end": "20270101T000000"}],
		"messages": [{"text": "Panne d'un ascenseur", "channel": {"types": ["title"]}}],
		"impacted_objects": [{"pt_object": {"id": "` + stopArea + `", "embedded_type": "stop_area"}}]}`
}

// siriVisit is a vehicle of a hand-written SIRI response; times are UTC.
type siriVisit struct {
	train, destination, aimed, expected, status string
}

// siriJSON writes a SIRI Lite stop-monitoring response in PRIM's shape.
func siriJSON(t *testing.T, visits []siriVisit) string {
	t.Helper()
	type obj = map[string]any
	var vs []obj
	for _, v := range visits {
		call := obj{"ExpectedDepartureTime": v.expected, "DepartureStatus": v.status}
		if v.aimed != "" {
			call["AimedDepartureTime"] = v.aimed
		}
		vs = append(vs, obj{"MonitoredVehicleJourney": obj{
			"LineRef":            obj{"value": "STIF:Line::C01743:"},
			"DirectionRef":       obj{"value": "Aller"},
			"DestinationName":    []obj{{"value": v.destination}},
			"VehicleJourneyName": []obj{{"value": v.train}},
			"TrainNumbers":       obj{"TrainNumberRef": []obj{{"value": v.train}}},
			"MonitoredCall":      call,
		}})
	}
	data, err := json.Marshal(obj{"Siri": obj{"ServiceDelivery": obj{
		"StopMonitoringDelivery": []obj{{"MonitoredStopVisit": vs}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// oneRide serves a journey whose only ride leaves Gare du Nord on RER B
// towards "Terminus" at dep, train trip, and the SIRI visits.
func oneRide(t *testing.T, e *env, dep, trip string, visits []siriVisit) {
	t.Helper()
	journeys := journeysJSON(t, []handJourney{{kind: "best", transfers: 0, walking: 0, durationS: 1200, rides: []ride{
		{from: gareDuNord, to: massy, dep: dep, code: "B", trip: trip, disruptions: nil},
	}}}, `[]`)
	e.fake.handle(navitia+"journeys", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, journeys) })
	body := siriJSON(t, visits)
	e.fake.handle(siri, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, body) })
}
