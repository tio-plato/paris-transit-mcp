package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tio-plato/paris-transit-mcp/internal/transit"
)

// Bounds of the integer parameters.
const (
	maxLimit = 10
	maxDays  = 30
)

// newServer returns an MCP server named paris-transit with the four tools.
func newServer(svc *transit.Service, version string) (*mcp.Server, error) {
	server := mcp.NewServer(&mcp.Implementation{
		Name: "paris-transit", Title: "Île-de-France public transport (PRIM)", Version: version,
	}, nil)
	for _, add := range []func(*mcp.Server, *transit.Service) error{
		addPlanJourney, addRankDestinations, addNextDepartures, addLineStatus,
	} {
		err := add(server, svc)
		if err != nil {
			return nil, err
		}
	}
	return server, nil
}

func addPlanJourney(server *mcp.Server, svc *transit.Service) error {
	in, err := schemaFor[transit.JourneyInput]()
	if err != nil {
		return err
	}
	describe(in, map[string]string{
		"origin":      placeDoc,
		"destination": placeDoc,
		"datetime": "When to leave, or to arrive by with datetime_represents=arrival, which requires it. " +
			timeDoc + " Default: now.",
		"datetime_represents": "departure (default) or arrival.",
		"profile": "standard (default), or step_free for luggage, a pushchair or a wheelchair: " +
			"lift-only routes, those without a broken lift first.",
	})
	nonEmpty(in, "origin", "destination")
	in.Properties["datetime_represents"].Enum = []any{"departure", "arrival"}
	in.Properties["datetime_represents"].Default = json.RawMessage(`"departure"`)
	in.Properties["profile"].Enum = []any{"standard", "step_free"}
	in.Properties["profile"].Default = json.RawMessage(`"standard"`)
	out, err := schemaFor[transit.JourneyOutput]()
	if err != nil {
		return err
	}
	mcp.AddTool(server, tool("plan_journey", planJourneyDoc, in, out), handler(svc.PlanJourney))
	return nil
}

func addRankDestinations(server *mcp.Server, svc *transit.Service) error {
	in, err := schemaFor[transit.RankInput]()
	if err != nil {
		return err
	}
	describe(in, map[string]string{
		"origin":       placeDoc,
		"destinations": "1 to 20 places to rank.",
		"depart_at":    "When to leave, for destinations without arrive_by. " + timeDoc + " Default: now.",
	})
	nonEmpty(in, "origin")
	destinations := in.Properties["destinations"]
	destinations.MinItems, destinations.MaxItems = jsonschema.Ptr(1), jsonschema.Ptr(transit.MaxDestinations)
	// Inferred slices also accept null, which would bypass the item counts.
	destinations.Type, destinations.Types = "array", nil
	describe(destinations.Items, map[string]string{
		"label":     "The client's name for this destination, returned as it is, such as the practitioner's name.",
		"place":     placeDoc + " Prefer coordinates when you have them.",
		"arrive_by": "Arrive by this time instead of leaving at depart_at. " + timeDoc,
	})
	nonEmpty(destinations.Items, "place")
	out, err := schemaFor[transit.RankOutput]()
	if err != nil {
		return err
	}
	mcp.AddTool(server, tool("rank_destinations", rankDoc, in, out), handler(svc.RankDestinations))
	return nil
}

func addNextDepartures(server *mcp.Server, svc *transit.Service) error {
	in, err := schemaFor[transit.DeparturesInput]()
	if err != nil {
		return err
	}
	describe(in, map[string]string{
		"stop":  placeDoc + " A place that is not a station stands for the nearest station within 500 m.",
		"lines": "0 to 10 lines, as names or line ids. " + lineDoc + " Default: up to 12 lines of the stop, rail first.",
		"limit": "Departures per line and direction, 1 to 10. Default: 3.",
	})
	nonEmpty(in, "stop")
	in.Properties["lines"].MaxItems = jsonschema.Ptr(transit.MaxLines)
	in.Properties["limit"].Minimum = jsonschema.Ptr(1.0)
	in.Properties["limit"].Maximum = jsonschema.Ptr(float64(maxLimit))
	in.Properties["limit"].Default = json.RawMessage(strconv.Itoa(transit.DefaultLimit))
	out, err := schemaFor[transit.DeparturesOutput]()
	if err != nil {
		return err
	}
	mcp.AddTool(server, tool("next_departures", departuresDoc, in, out), handler(svc.NextDepartures))
	return nil
}

func addLineStatus(server *mcp.Server, svc *transit.Service) error {
	in, err := schemaFor[transit.StatusInput]()
	if err != nil {
		return err
	}
	describe(in, map[string]string{
		"lines": "1 to 10 lines, as names or line ids. " + lineDoc,
		"days":  "Report works that start within this many days, 1 to 30. Default: 7.",
	})
	lines := in.Properties["lines"]
	lines.MinItems, lines.MaxItems = jsonschema.Ptr(1), jsonschema.Ptr(transit.MaxLines)
	lines.Type, lines.Types = "array", nil
	in.Properties["days"].Minimum = jsonschema.Ptr(1.0)
	in.Properties["days"].Maximum = jsonschema.Ptr(float64(maxDays))
	in.Properties["days"].Default = json.RawMessage(strconv.Itoa(transit.DefaultDays))
	out, err := schemaFor[transit.StatusOutput]()
	if err != nil {
		return err
	}
	mcp.AddTool(server, tool("line_status", statusDoc, in, out), handler(svc.LineStatus))
	return nil
}

// schemaFor infers the schema of T.
func schemaFor[T any]() (*jsonschema.Schema, error) {
	s, err := jsonschema.For[T](&jsonschema.ForOptions{IgnoreInvalidTypes: false, TypeSchemas: nil})
	if err != nil {
		return nil, fmt.Errorf("infer schema: %w", err)
	}
	return s, nil
}

func describe(s *jsonschema.Schema, docs map[string]string) {
	for name, doc := range docs {
		s.Properties[name].Description = doc
	}
}

// nonEmpty makes string properties refuse the empty string.
func nonEmpty(s *jsonschema.Schema, names ...string) {
	for _, name := range names {
		s.Properties[name].MinLength = jsonschema.Ptr(1)
	}
}

func tool(name, description string, in, out *jsonschema.Schema) *mcp.Tool {
	return &mcp.Tool{
		Name:         name,
		Description:  description,
		InputSchema:  in,
		OutputSchema: out,
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true},
	}
}

// output is a tool result that carries its error.
type output[T any] interface {
	*T
	SetError(e *transit.ToolError)
}

// handler adapts a tool function to the SDK. An error makes an MCP error
// result (isError) that carries the error object; an answer such as
// not_found is a normal result, set by the tool.
func handler[In, Out any, P output[Out]](f func(context.Context, In) (*Out, error)) mcp.ToolHandlerFor[In, *Out] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, *Out, error) {
		out, err := f(ctx, in)
		if err != nil {
			var failed Out
			P(&failed).SetError(transit.AsToolError(err))
			return &mcp.CallToolResult{IsError: true}, &failed, nil
		}
		return nil, out, nil
	}
}
