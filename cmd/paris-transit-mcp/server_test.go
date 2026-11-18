package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tio-plato/paris-transit-mcp/internal/prim"
	"github.com/tio-plato/paris-transit-mcp/internal/transit"
)

// fixtures maps request paths, and for /places and /pt_objects the q
// parameter, to recorded PRIM responses.
func fixtures() map[string]string {
	return map[string]string{
		"/v2/navitia/places?q=Gare du Nord":                      "20261118T0520-places-gare-du-nord.json",
		"/v2/navitia/places?q=Massy-Palaiseau":                   "20261118T0520-places-massy-palaiseau.json",
		"/v2/navitia/places?q=Gare Saint-Lazare":                 "20261118T0520-places-gare-saint-lazare.json",
		"/v2/navitia/places?q=12 rue Victor Hugo":                "20261118T0519-places-12-rue-victor-hugo.json",
		"/v2/navitia/journeys":                                   "20261118T0520-journey-gare-du-nord-massy.json",
		"/stop-monitoring":                                       "20261118T0521-siri-saint-lazare-j.json",
		"/v2/navitia/stop_areas/stop_area:IDFM:71370/lines":      "20261118T0525-lines-saint-lazare.json",
		"/v2/navitia/stop_areas/stop_area:IDFM:71370/departures": "20261118T0525-departures-saint-lazare-n151.json",
		"/v2/navitia/pt_objects?q=b":                             "20261118T0523-pt-objects-b.json",
	}
}

type fakePRIM struct {
	t        *testing.T
	requests atomic.Int64
}

func (f *fakePRIM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	key := r.URL.Path
	if q := r.URL.Query().Get("q"); q != "" {
		key += "?q=" + q
	}
	name, ok := fixtures()[key]
	w.Header().Set("Content-Type", "application/json")
	switch {
	case ok:
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
		if err != nil {
			f.t.Error(err)
		}
		_, _ = w.Write(data)
	case strings.HasPrefix(r.URL.Path, "/v2/navitia/line_reports/"):
		_, _ = io.WriteString(w, `{"line_reports": [], "disruptions": []}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error": {"id": "unknown_object", "message": "not in the fake"}}`)
	}
}

// connect serves the tools over the SDK's in-memory transport, so the SDK
// validates the arguments and the results against the tools' schemas.
func connect(t *testing.T) (*mcp.ClientSession, *fakePRIM) {
	t.Helper()
	fake := &fakePRIM{t: t}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 5, 21, 0, 0, paris)
	client := prim.New(prim.Config{BaseURL: srv.URL, Token: "test-token", Timeout: 0, Rate: 1000})
	svc, err := transit.New(client, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	server, err := newServer(svc, "test")
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	cs, err := c.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, fake
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v", name, err)
	}
	var structured map[string]any
	data, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(data, &structured)
	return res, structured
}

func TestToolsThroughTheSDK(t *testing.T) {
	t.Parallel()
	cs, _ := connect(t)

	for _, tc := range []struct {
		name string
		args map[string]any
		// key is a field the structured result must hold.
		key string
	}{
		{"plan_journey", map[string]any{"origin": "Gare du Nord", "destination": "Massy-Palaiseau"}, "journeys"},
		{"rank_destinations", map[string]any{"origin": "Gare du Nord", "destinations": []any{
			map[string]any{"label": "Massy", "place": "Massy-Palaiseau"},
		}}, "ranked"},
		{"next_departures", map[string]any{"stop": "Gare Saint-Lazare", "lines": []string{"J"}}, "lines"},
		{"next_departures", map[string]any{"stop": "Gare Saint-Lazare"}, "skipped_lines"},
		{"line_status", map[string]any{"lines": []string{"RER B"}}, "lines"},
	} {
		res, structured := call(t, cs, tc.name, tc.args)
		if res.IsError {
			t.Errorf("%s: tool error %+v", tc.name, res.Content)
			continue
		}
		if _, ok := structured[tc.key]; !ok {
			t.Errorf("%s: structured result %v has no %s", tc.name, structured, tc.key)
		}
	}
}

func TestQueryAnswerIsNotAnMCPError(t *testing.T) {
	t.Parallel()
	cs, _ := connect(t)

	res, structured := call(t, cs, "plan_journey",
		map[string]any{"origin": "12 rue Victor Hugo", "destination": "Massy-Palaiseau"})

	if res.IsError {
		t.Errorf("ambiguous_place is an MCP error: %+v", res.Content)
	}
	e, _ := structured["error"].(map[string]any)
	if e["code"] != transit.CodeAmbiguousPlace {
		t.Errorf("structured result %v, want error ambiguous_place", structured)
	}
}

func TestBadTimeIsAnMCPErrorWithItsCode(t *testing.T) {
	t.Parallel()
	cs, fake := connect(t)

	res, structured := call(t, cs, "plan_journey", map[string]any{
		"origin": "Gare du Nord", "destination": "Massy-Palaiseau", "datetime": "tomorrow morning",
	})

	if !res.IsError {
		t.Errorf("bad_request is not an MCP error: %v", structured)
	}
	e, _ := structured["error"].(map[string]any)
	if e["code"] != transit.CodeBadRequest {
		t.Errorf("structured result %v, want error bad_request", structured)
	}
	if n := fake.requests.Load(); n != 0 {
		t.Errorf("sent %d PRIM requests", n)
	}
}

func TestRankSchemaRefusesTooManyOrNullDestinations(t *testing.T) {
	t.Parallel()
	cs, fake := connect(t)

	many := make([]any, transit.MaxDestinations+1)
	for i := range many {
		many[i] = map[string]any{"label": "x", "place": "Massy-Palaiseau"}
	}
	for _, destinations := range []any{many, nil} {
		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "rank_destinations", Arguments: map[string]any{
			"origin": "Gare du Nord", "destinations": destinations,
		}})
		if err != nil || !res.IsError {
			t.Errorf("destinations %v accepted (%v)", destinations, err)
			continue
		}
		// The SDK refuses them by the input schema, before the tool runs.
		if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "validating") {
			t.Errorf("destinations %v: error %q is not a schema validation error", destinations, text)
		}
	}
	if n := fake.requests.Load(); n != 0 {
		t.Errorf("sent %d PRIM requests", n)
	}
}
