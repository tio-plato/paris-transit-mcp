package prim_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tio-plato/paris-transit-mcp/internal/prim"
)

const testToken = "test-token-0123456789abcdef"

func TestRequestCarriesTheTokenOnlyInTheHeader(t *testing.T) {
	t.Parallel()
	var got http.Header
	var rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		rawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	token, err := prim.TokenFromEnv(func(string) string { return testToken })
	if err != nil {
		t.Fatal(err)
	}
	c := prim.New(prim.Config{BaseURL: srv.URL, Token: token, Timeout: 0, Rate: 0})

	err = c.Navitia(t.Context(), "places", nil, &struct{}{})

	if h := got.Get("Apikey"); h != testToken {
		t.Errorf("apiKey header = %q, want the PRIM_API_KEY value", h)
	}
	if strings.Contains(rawQuery, testToken) {
		t.Errorf("the token is in the query %q", rawQuery)
	}
	if err == nil {
		t.Fatal("HTTP 503 gave no error")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error %q holds the token", err)
	}
}

func TestTokenFromEnvRefusesAMissingToken(t *testing.T) {
	t.Parallel()
	_, err := prim.TokenFromEnv(func(string) string { return "" })
	if err == nil {
		t.Fatal("no error for an unset PRIM_API_KEY")
	}
}

func TestTooManyRequestsIsAQuotaError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	c := prim.New(prim.Config{BaseURL: srv.URL, Token: testToken, Timeout: 0, Rate: 0})

	_, err := c.StopMonitoring(t.Context(), "STIF:StopArea:SP:71370:", "STIF:Line::C01739:")

	var perr *prim.Error
	if !errors.As(err, &perr) || perr.Code != prim.CodeQuota {
		t.Fatalf("error = %v, want a %s error", err, prim.CodeQuota)
	}
}

func TestNavitiaIDsToSIRIRefs(t *testing.T) {
	t.Parallel()
	stop, ok := prim.SIRIStopRef("stop_area:IDFM:71264")
	if !ok || stop != "STIF:StopArea:SP:71264:" {
		t.Errorf("SIRIStopRef = %q, %v", stop, ok)
	}
	line, ok := prim.SIRILineRef("line:IDFM:C01739")
	if !ok || line != "STIF:Line::C01739:" {
		t.Errorf("SIRILineRef = %q, %v", line, ok)
	}
}
