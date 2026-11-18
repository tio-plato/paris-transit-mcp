package prim_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestTokenFromEnvRefusesAnUnexpandedReference(t *testing.T) {
	t.Parallel()
	// A client that cannot expand ${PRIM_API_KEY} passes the reference as it is.
	_, err := prim.TokenFromEnv(func(string) string { return "${PRIM_API_KEY}" })
	if err == nil {
		t.Fatal("no error for an unexpanded ${PRIM_API_KEY}")
	}
}

func TestConcurrentRequestsArePacedByTheRate(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var arrivals []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	const rate, n = 20, 6
	c := prim.New(prim.Config{BaseURL: srv.URL, Token: testToken, Timeout: 0, Rate: rate})

	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { _ = c.Navitia(t.Context(), "places", nil, &struct{}{}) })
	}
	wg.Wait()

	slices.SortFunc(arrivals, func(a, b time.Time) int { return a.Compare(b) })
	// n requests at rate a second span at least n-1 intervals, whatever their
	// concurrency; a small margin absorbs the scheduler.
	minSpan := time.Duration(n-1) * time.Second / rate * 9 / 10
	if span := arrivals[n-1].Sub(arrivals[0]); span < minSpan {
		t.Errorf("%d concurrent requests arrived within %v, want at least %v at %d a second", n, span, minSpan, rate)
	}
}
