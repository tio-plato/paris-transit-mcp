// Package transit implements the four tools.
package transit

import (
	"errors"
	"fmt"
	"time"
	_ "time/tzdata" // don't depend on the host's zoneinfo

	"github.com/tio-plato/paris-transit-mcp/internal/prim"
)

// Error codes. The first four are MCP errors (isError), the rest are answers.
const (
	CodeAuth           = prim.CodeAuth
	CodeQuota          = prim.CodeQuota
	CodeUpstream       = prim.CodeUpstream
	CodeBadRequest     = prim.CodeBadRequest
	CodeNotFound       = prim.CodeNotFound
	CodeNoTransitRoute = "no_transit_route"
	CodeAmbiguousPlace = "ambiguous_place"
	CodeAmbiguousLine  = "ambiguous_line"
)

func IsErrorCode(code string) bool {
	switch code {
	case CodeAuth, CodeQuota, CodeUpstream, CodeBadRequest:
		return true
	default:
		return false
	}
}

type ToolError struct {
	Code       string      `json:"code"`
	Message    string      `json:"message"`
	Candidates []Candidate `json:"candidates,omitempty"`

	cause error
}

func (e *ToolError) Error() string { return e.Message }

func (e *ToolError) Unwrap() error { return e.cause }

// Candidate is a place (id … lon) or a line (line_id … network).
type Candidate struct {
	ID      string  `json:"id,omitempty"`
	Name    string  `json:"name,omitempty"`
	Type    string  `json:"type,omitempty"`
	Town    string  `json:"town,omitempty"`
	Lat     float64 `json:"lat,omitempty"`
	Lon     float64 `json:"lon,omitempty"`
	LineID  string  `json:"line_id,omitempty"`
	Line    string  `json:"line,omitempty"`
	Mode    string  `json:"mode,omitempty"`
	Network string  `json:"network,omitempty"`
}

// AsToolError keeps a PRIM error's code; anything else is upstream.
func AsToolError(err error) *ToolError {
	var te *ToolError
	if errors.As(err, &te) {
		return te
	}
	var pe *prim.Error
	if errors.As(err, &pe) {
		return &ToolError{Code: pe.Code, Message: err.Error(), Candidates: nil}
	}
	return &ToolError{Code: CodeUpstream, Message: err.Error(), Candidates: nil}
}

// failure wraps an error about a stop or line we already resolved. A 404
// without Navitia's error body comes from the gateway, so it's a failure,
// not not_found.
func failure(err error, format string, args ...any) error {
	what := fmt.Sprintf(format, args...)
	if gatewayNotFound(err) {
		return &ToolError{Code: CodeUpstream, Message: what + ": " + err.Error(), Candidates: nil, cause: err}
	}
	return fmt.Errorf("%s: %w", what, err)
}

func navitiaNotFound(err error) bool {
	return AsToolError(err).Code == CodeNotFound && navitiaErrorID(err) != ""
}

func gatewayNotFound(err error) bool {
	return AsToolError(err).Code == CodeNotFound && navitiaErrorID(err) == ""
}

func navitiaErrorID(err error) string {
	var pe *prim.Error
	if errors.As(err, &pe) {
		return pe.NavitiaID
	}
	return ""
}

// stopsCall: after auth or quota errors every further request fails too.
func stopsCall(err error) bool {
	code := AsToolError(err).Code
	return code == CodeAuth || code == CodeQuota
}

func newError(code, format string, args ...any) *ToolError {
	return &ToolError{Code: code, Message: fmt.Sprintf(format, args...), Candidates: nil}
}

type Resolved struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Type        string       `json:"type"`
	Lat         float64      `json:"lat"`
	Lon         float64      `json:"lon"`
	SnappedFrom *SnappedFrom `json:"snapped_from,omitempty"`
}

type SnappedFrom struct {
	Name      string `json:"name"`
	DistanceM int    `json:"distance_m"`
}

type Endpoint struct {
	Resolved Resolved `json:"resolved"`
}

type Service struct {
	prim  *prim.Client
	now   func() time.Time
	paris *time.Location
}

func New(client *prim.Client, now func() time.Time) (*Service, error) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		return nil, fmt.Errorf("load the Europe/Paris time zone: %w", err)
	}
	return &Service{prim: client, now: now, paris: paris}, nil
}
