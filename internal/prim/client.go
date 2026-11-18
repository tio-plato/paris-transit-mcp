// Package prim is a client for Île-de-France Mobilités' PRIM APIs: the Navitia
// journey planner and the SIRI Lite stop monitoring.
package prim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// DefaultBaseURL is PRIM's marketplace root.
const DefaultBaseURL = "https://prim.iledefrance-mobilites.fr/marketplace"

// KeyVariable is the environment variable that holds the PRIM token.
const KeyVariable = "PRIM_API_KEY"

const (
	// RequestsPerSecond stays below PRIM's limit of 5 requests a second per API.
	RequestsPerSecond = 4
	defaultTimeout    = 20 * time.Second
	maxBody           = 16 << 20
)

// Error codes the client returns.
const (
	CodeAuth       = "auth"
	CodeQuota      = "quota"
	CodeUpstream   = "upstream"
	CodeBadRequest = "bad_request"
	CodeNotFound   = "not_found"
)

// Error is a failed PRIM request. Its message never holds the token.
type Error struct {
	Code    string
	Message string
	// NavitiaID is the id of the error object in a Navitia error body, such as
	// no_solution, when there is one.
	NavitiaID string
}

func (e *Error) Error() string { return e.Message }

// Config configures a [Client].
type Config struct {
	BaseURL string
	Token   string
	// Timeout bounds each request; zero means 20 seconds.
	Timeout time.Duration
	// Rate is the number of requests a second per API family; zero means
	// [RequestsPerSecond].
	Rate float64
}

// Client sends requests to PRIM. It is safe for concurrent use.
type Client struct {
	base    string
	token   string
	http    *http.Client
	navitia *rate.Limiter
	siri    *rate.Limiter
}

// TokenFromEnv reads the PRIM token with getenv.
func TokenFromEnv(getenv func(string) string) (string, error) {
	token := strings.TrimSpace(getenv(KeyVariable))
	if token == "" {
		return "", fmt.Errorf("environment variable %s is not set", KeyVariable)
	}
	// An MCP client passes a ${VAR} reference as it is when VAR is unset in
	// its own environment.
	// The value is not repeated: a fallback written in the reference may be
	// the token itself.
	if strings.HasPrefix(token, "${") {
		return "", fmt.Errorf("environment variable %s holds an unexpanded ${…} reference: "+
			"set the variable in the environment of the MCP client", KeyVariable)
	}
	return token, nil
}

// New returns a client.
func New(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	perSecond := cfg.Rate
	if perSecond == 0 {
		perSecond = RequestsPerSecond
	}
	return &Client{
		base:  strings.TrimRight(cfg.BaseURL, "/"),
		token: cfg.Token,
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		// A burst of one spaces requests evenly, so no second ever holds more
		// than the rate.
		navitia: rate.NewLimiter(rate.Limit(perSecond), 1),
		siri:    rate.NewLimiter(rate.Limit(perSecond), 1),
	}
}

// navitiaPath matches the path segments the client builds: ids and
// coordinates, never a dot-dot or a query.
var navitiaPath = regexp.MustCompile(`^[A-Za-z0-9_]+(/[A-Za-z0-9:;._-]+)*$`)

// Navitia sends GET /v2/navitia/<path> with query and decodes the JSON body into out.
func (c *Client) Navitia(ctx context.Context, path string, query url.Values, out any) error {
	if !navitiaPath.MatchString(path) || strings.Contains(path, "..") {
		return &Error{Code: CodeBadRequest, Message: fmt.Sprintf("invalid Navitia path %q", path)}
	}
	return c.get(ctx, c.navitia, "/v2/navitia/"+path, query, out)
}

// StopMonitoring sends a SIRI Lite stop-monitoring request for one stop and one line.
func (c *Client) StopMonitoring(ctx context.Context, monitoringRef, lineRef string) (*StopMonitoring, error) {
	query := url.Values{"MonitoringRef": {monitoringRef}}
	if lineRef != "" {
		query.Set("LineRef", lineRef)
	}
	var sm StopMonitoring
	err := c.get(ctx, c.siri, "/stop-monitoring", query, &sm)
	if err != nil {
		return nil, err
	}
	return &sm, nil
}

func (c *Client) get(ctx context.Context, limiter *rate.Limiter, path string, query url.Values, out any) error {
	err := limiter.Wait(ctx)
	if err != nil {
		return &Error{Code: CodeUpstream, Message: fmt.Sprintf("wait for the PRIM rate limit on %s: %v", path, err)}
	}
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return &Error{Code: CodeBadRequest, Message: fmt.Sprintf("build the request for %s: %v", path, err)}
	}
	// PRIM documents the header as apiKey: it is set as written, not in
	// the canonical form Header.Set would give it.
	req.Header["apiKey"] = []string{c.token}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// The error names the URL, which holds no token: the token is a header.
		return &Error{Code: CodeUpstream, Message: fmt.Sprintf("reach PRIM for %s: %v", path, unwrapURLError(err))}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return &Error{Code: CodeUpstream, Message: fmt.Sprintf("read the PRIM response for %s: %v", path, err)}
	}
	if resp.StatusCode != http.StatusOK {
		return statusError(path, resp.StatusCode, body)
	}
	err = json.Unmarshal(body, out)
	if err != nil {
		return &Error{Code: CodeUpstream, Message: fmt.Sprintf("decode the PRIM response for %s: %v", path, err)}
	}
	return nil
}

// unwrapURLError drops the [url.Error] wrapper, whose text repeats the URL.
func unwrapURLError(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		if uerr.Timeout() {
			return errors.New("timeout")
		}
		return uerr.Err
	}
	return err
}

// navitiaError is the body Navitia sends with a 4xx status.
type navitiaError struct {
	Error struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"error"`
}

func statusError(path string, status int, body []byte) *Error {
	var nerr navitiaError
	_ = json.Unmarshal(body, &nerr)
	detail := ""
	if nerr.Error.ID != "" {
		detail = fmt.Sprintf(" (%s: %s)", nerr.Error.ID, nerr.Error.Message)
	}
	e := &Error{NavitiaID: nerr.Error.ID}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.Code = CodeAuth
		e.Message = fmt.Sprintf("PRIM refused the token (HTTP %d on %s): check %s and the API subscriptions",
			status, path, KeyVariable)
	case status == http.StatusTooManyRequests:
		e.Code = CodeQuota
		e.Message = fmt.Sprintf("PRIM refused the request for its daily quota or its rate limit "+
			"(HTTP 429 on %s): wait before asking again", path)
	case status == http.StatusNotFound:
		e.Code = CodeNotFound
		e.Message = fmt.Sprintf("PRIM found nothing for %s%s", path, detail)
	case status >= 400 && status < 500:
		e.Code = CodeBadRequest
		e.Message = fmt.Sprintf("PRIM rejected the request (HTTP %d on %s)%s", status, path, detail)
	default:
		e.Code = CodeUpstream
		e.Message = fmt.Sprintf("PRIM failed (HTTP %d on %s)", status, path)
	}
	return e
}

var (
	stopAreaID = regexp.MustCompile(`^stop_area:IDFM:([A-Za-z0-9]+)$`)
	lineID     = regexp.MustCompile(`^line:IDFM:([A-Za-z0-9]+)$`)
)

// SIRIStopRef turns a Navitia stop area id (stop_area:IDFM:71264) into a SIRI
// MonitoringRef (STIF:StopArea:SP:71264:).
func SIRIStopRef(stopArea string) (string, bool) {
	m := stopAreaID.FindStringSubmatch(stopArea)
	if m == nil {
		return "", false
	}
	return "STIF:StopArea:SP:" + m[1] + ":", true
}

// SIRILineRef turns a Navitia line id (line:IDFM:C01739) into a SIRI LineRef
// (STIF:Line::C01739:).
func SIRILineRef(line string) (string, bool) {
	m := lineID.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return "STIF:Line::" + m[1] + ":", true
}
