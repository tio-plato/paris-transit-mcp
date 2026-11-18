// Package transit holds the four read-only tools: it resolves places, lines and
// times, queries PRIM and decides on the server side what the client would
// otherwise have to guess.
package transit

import (
	"errors"
	"fmt"
	"time"
	_ "time/tzdata" // Paris times must not depend on the host's zoneinfo

	"github.com/tio-plato/paris-transit-mcp/internal/prim"
)

// Error codes of a tool result. The first four make the result an MCP error
// (isError); the others are answers to the query.
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

// IsErrorCode reports whether a result with this code is an MCP error result.
func IsErrorCode(code string) bool {
	switch code {
	case CodeAuth, CodeQuota, CodeUpstream, CodeBadRequest:
		return true
	default:
		return false
	}
}

// ToolError is the error object of a tool result.
type ToolError struct {
	Code       string      `json:"code"`
	Message    string      `json:"message"`
	Candidates []Candidate `json:"candidates,omitempty"`
}

func (e *ToolError) Error() string { return e.Message }

// Candidate is a choice offered with ambiguous_place (id, name, type, town,
// lat, lon) or ambiguous_line (line_id, line, mode, network).
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

// AsToolError turns any error into a tool error: PRIM errors keep their code,
// any other error is an upstream failure.
func AsToolError(err error) *ToolError {
	var te *ToolError
	if errors.As(err, &te) {
		return te
	}
	var pe *prim.Error
	if errors.As(err, &pe) {
		// The whole chain says what the tool was doing when PRIM failed.
		return &ToolError{Code: pe.Code, Message: err.Error(), Candidates: nil}
	}
	return &ToolError{Code: CodeUpstream, Message: err.Error(), Candidates: nil}
}

// failure wraps err from a request about a stop or line already resolved:
// there, a 404 is PRIM failing, not an answer that the thing does not exist.
func failure(err error, format string, args ...any) error {
	what := fmt.Sprintf(format, args...)
	if te := AsToolError(err); te.Code == CodeNotFound {
		return &ToolError{Code: CodeUpstream, Message: what + ": " + te.Message, Candidates: nil}
	}
	return fmt.Errorf("%s: %w", what, err)
}

// navitiaErrorID is the id of the Navitia error body behind err, such as no_solution.
func navitiaErrorID(err error) string {
	var pe *prim.Error
	if errors.As(err, &pe) {
		return pe.NavitiaID
	}
	return ""
}

// stopsCall reports whether err ends the whole call: PRIM refused the token
// or the quota is spent, so every further request would fail too.
func stopsCall(err error) bool {
	code := AsToolError(err).Code
	return code == CodeAuth || code == CodeQuota
}

func newError(code, format string, args ...any) *ToolError {
	return &ToolError{Code: code, Message: fmt.Sprintf(format, args...), Candidates: nil}
}

// Resolved is what a place parameter was understood as.
type Resolved struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Type        string       `json:"type"`
	Lat         float64      `json:"lat"`
	Lon         float64      `json:"lon"`
	SnappedFrom *SnappedFrom `json:"snapped_from,omitempty"`
}

// SnappedFrom names the place the server replaced by a nearby station.
type SnappedFrom struct {
	Name      string `json:"name"`
	DistanceM int    `json:"distance_m"`
}

// Endpoint wraps the resolved place of a parameter.
type Endpoint struct {
	Resolved Resolved `json:"resolved"`
}

// Service runs the tools.
type Service struct {
	prim  *prim.Client
	now   func() time.Time
	paris *time.Location
}

// New returns the service; now is the clock that sets "now" for times and realtime.
func New(client *prim.Client, now func() time.Time) (*Service, error) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		return nil, fmt.Errorf("load the Europe/Paris time zone: %w", err)
	}
	return &Service{prim: client, now: now, paris: paris}, nil
}
