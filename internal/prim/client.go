// Package prim is a client for the PRIM APIs: Navitia and SIRI Lite.
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

const DefaultBaseURL = "https://prim.iledefrance-mobilites.fr/marketplace"

const KeyVariable = "PRIM_API_KEY"

const (
	// RequestsPerSecond stays under PRIM's limit of 5 per API.
	RequestsPerSecond = 4
	defaultTimeout    = 20 * time.Second
	maxBody           = 16 << 20
)

const (
	CodeAuth       = "auth"
	CodeQuota      = "quota"
	CodeUpstream   = "upstream"
	CodeBadRequest = "bad_request"
	CodeNotFound   = "not_found"
)

// Error is a failed request. Its message never contains the token.
type Error struct {
	Code    string
	Message string
	// NavitiaID is the error id from Navitia's body, e.g. no_solution.
	NavitiaID string
}

func (e *Error) Error() string { return e.Message }

type Config struct {
	BaseURL string
	Token   string
	Timeout time.Duration
	Rate    float64
}

type Client struct {
	base    string
	token   string
	http    *http.Client
	navitia *rate.Limiter
	siri    *rate.Limiter
}

func TokenFromEnv(getenv func(string) string) (string, error) {
	token := strings.TrimSpace(getenv(KeyVariable))
	if token == "" {
		return "", fmt.Errorf("environment variable %s is not set", KeyVariable)
	}
	// Clients pass ${VAR} through when VAR is unset. Don't echo it: a
	// ${VAR:-fallback} may hold the token.
	if strings.HasPrefix(token, "${") {
		return "", fmt.Errorf("environment variable %s holds an unexpanded ${…} reference: "+
			"set the variable in the environment of the MCP client", KeyVariable)
	}
	return token, nil
}

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
		// Burst 1, so no one-second window exceeds the rate.
		navitia: rate.NewLimiter(rate.Limit(perSecond), 1),
		siri:    rate.NewLimiter(rate.Limit(perSecond), 1),
	}
}

// navitiaPath allows ids and coordinates, nothing like ".." or "?".
var navitiaPath = regexp.MustCompile(`^[A-Za-z0-9_]+(/[A-Za-z0-9:;._-]+)*$`)

func (c *Client) Navitia(ctx context.Context, path string, query url.Values, out any) error {
	if !navitiaPath.MatchString(path) || strings.Contains(path, "..") {
		return &Error{Code: CodeBadRequest, Message: fmt.Sprintf("invalid Navitia path %q", path)}
	}
	return c.get(ctx, c.navitia, "/v2/navitia/"+path, query, out)
}

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
	// Set directly: PRIM documents "apiKey", Header.Set would send "Apikey".
	req.Header["apiKey"] = []string{c.token}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
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

// unwrapURLError drops [url.Error], which repeats the URL.
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

// SIRIStopRef maps stop_area:IDFM:71264 to STIF:StopArea:SP:71264:.
func SIRIStopRef(stopArea string) (string, bool) {
	m := stopAreaID.FindStringSubmatch(stopArea)
	if m == nil {
		return "", false
	}
	return "STIF:StopArea:SP:" + m[1] + ":", true
}

// SIRILineRef maps line:IDFM:C01739 to STIF:Line::C01739:.
func SIRILineRef(line string) (string, bool) {
	m := lineID.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return "STIF:Line::" + m[1] + ":", true
}
