package transit

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// maxWalk is the longest walk-only trip the server gives as an answer: a
	// longer one most likely comes from a place Navitia could not reach.
	maxWalk = 30 * time.Minute

	profileStandard = "standard"
	profileStepFree = "step_free"

	representsDeparture = "departure"
	representsArrival   = "arrival"
)

// JourneyInput is the input of plan_journey.
type JourneyInput struct {
	Origin             string `json:"origin"`
	Destination        string `json:"destination"`
	Datetime           string `json:"datetime,omitempty"`
	DatetimeRepresents string `json:"datetime_represents,omitempty"`
	Profile            string `json:"profile,omitempty"`
}

// JourneyOutput is the result of plan_journey.
type JourneyOutput struct {
	Origin      *Endpoint  `json:"origin,omitempty"`
	Destination *Endpoint  `json:"destination,omitempty"`
	Journeys    []Journey  `json:"journeys,omitempty"`
	Error       *ToolError `json:"error,omitempty"`
}

// SetError sets the result's error.
func (o *JourneyOutput) SetError(e *ToolError) { o.Error = e }

// Journey is one way to make the trip.
type Journey struct {
	Kind             string            `json:"kind"`
	Departure        string            `json:"departure"`
	Arrival          string            `json:"arrival"`
	DurationMin      int               `json:"duration_min"`
	Transfers        int               `json:"transfers"`
	WalkingMin       int               `json:"walking_min"`
	Legs             []Leg             `json:"legs"`
	FirstLegRealtime *FirstLegRealtime `json:"first_leg_realtime"`
	Disruptions      Disruptions       `json:"disruptions"`
	CitymapperURL    string            `json:"citymapper_url"`
}

// Leg is one public transport ride of a journey.
type Leg struct {
	Mode      string `json:"mode"`
	Line      string `json:"line"`
	LineID    string `json:"line_id"`
	Direction string `json:"direction"`
	From      string `json:"from"`
	FromID    string `json:"from_id"`
	To        string `json:"to"`
	ToID      string `json:"to_id"`
	Departure string `json:"departure"`
	Arrival   string `json:"arrival"`
	Stops     int    `json:"stops"`
}

// journeyQuery is a request to Navitia's journey planner.
type journeyQuery struct {
	from, to place
	when     time.Time
	arrival  bool
	stepFree bool
	// max is max_nb_journeys; zero leaves PRIM's default.
	max int
}

// resultError splits an error into one the result carries (a query answer
// such as not_found) and one that makes the call an MCP error.
func resultError(err error) (*ToolError, error) {
	te := AsToolError(err)
	if IsErrorCode(te.Code) {
		return nil, err
	}
	return te, nil
}

// PlanJourney runs plan_journey.
func (s *Service) PlanJourney(ctx context.Context, in JourneyInput) (*JourneyOutput, error) {
	out, err := s.planJourney(ctx, in)
	if err != nil {
		te, callErr := resultError(err)
		if callErr != nil {
			return nil, callErr
		}
		if out == nil {
			out = &JourneyOutput{Origin: nil, Destination: nil, Journeys: nil, Error: nil}
		}
		out.Error = te
	}
	return out, nil
}

func (s *Service) planJourney(ctx context.Context, in JourneyInput) (*JourneyOutput, error) {
	q, err := s.journeyOptions(in)
	if err != nil {
		return nil, err
	}
	q.from, err = s.resolvePlace(ctx, "origin", in.Origin)
	if err != nil {
		return nil, err
	}
	q.to, err = s.resolvePlace(ctx, "destination", in.Destination)
	if err != nil {
		return &JourneyOutput{Origin: q.from.endpoint(), Destination: nil, Journeys: nil, Error: nil}, err
	}
	// Citymapper gets the places as asked, the user's own position included,
	// even when PRIM is asked from a nearby station.
	asked := q
	resp, err := s.journeys(ctx, &q)
	out := &JourneyOutput{Origin: q.from.endpoint(), Destination: q.to.endpoint(), Journeys: nil, Error: nil}
	if err != nil {
		return out, err
	}
	var arrival time.Time
	if q.arrival {
		arrival = q.when
	}
	link := s.citymapperURL(&asked.from.Resolved, &asked.to.Resolved, arrival)
	disruptions := map[string]*navDisruption{}
	for i := range resp.Disruptions {
		disruptions[resp.Disruptions[i].ID] = &resp.Disruptions[i]
	}
	rt := realtimeLookup{s: s, cache: map[string]lookup{}}
	type ranked struct {
		journey   Journey
		cancelled bool
	}
	list := make([]ranked, 0, len(resp.Journeys))
	for i := range resp.Journeys {
		j := &resp.Journeys[i]
		// PRIM may add a walk of hours beside public transport: it is no
		// alternative, as the walk-only rule of journeys says.
		if !answer(j) {
			continue
		}
		first, cancelled, rtErr := rt.firstLeg(ctx, j)
		if rtErr != nil {
			return nil, rtErr
		}
		list = append(list, ranked{
			journey: Journey{
				Kind:             j.Type,
				Departure:        s.isoNavitia(j.DepartureDateTime),
				Arrival:          s.isoNavitia(j.ArrivalDateTime),
				DurationMin:      roundMinutes(j.Duration),
				Transfers:        j.NbTransfers,
				WalkingMin:       roundMinutes(j.Durations.Walking),
				Legs:             s.legs(j),
				FirstLegRealtime: first,
				Disruptions:      s.journeyDisruptions(j, disruptions),
				CitymapperURL:    link,
			},
			cancelled: cancelled,
		})
	}
	// A journey whose first train is cancelled goes last; step-free journeys
	// are ordered by the server, the others keep PRIM's order.
	slices.SortStableFunc(list, func(a, b ranked) int {
		if c := compareBool(a.cancelled, b.cancelled); c != 0 || !q.stepFree {
			return c
		}
		x, y := &a.journey, &b.journey
		return cmp.Or(
			compareBool(len(x.Disruptions.Equipment) > 0, len(y.Disruptions.Equipment) > 0),
			cmp.Compare(x.Transfers, y.Transfers),
			cmp.Compare(x.WalkingMin, y.WalkingMin),
			cmp.Compare(x.DurationMin, y.DurationMin),
		)
	})
	for _, r := range list {
		out.Journeys = append(out.Journeys, r.journey)
	}
	return out, nil
}

// journeyOptions reads the time, its meaning and the profile of a journey.
func (s *Service) journeyOptions(in JourneyInput) (journeyQuery, error) {
	q := journeyQuery{from: place{}, to: place{}, when: s.now(), arrival: false, stepFree: false, max: 0}
	switch in.DatetimeRepresents {
	case "", representsDeparture:
	case representsArrival:
		q.arrival = true
	default:
		return q, newError(CodeBadRequest, "datetime_represents %q is neither departure nor arrival",
			in.DatetimeRepresents)
	}
	switch in.Profile {
	case "", profileStandard:
	case profileStepFree:
		q.stepFree = true
	default:
		return q, newError(CodeBadRequest, "profile %q is neither standard nor step_free", in.Profile)
	}
	if in.Datetime == "" && q.arrival {
		return q, newError(CodeBadRequest, "datetime_represents arrival needs a datetime to arrive by")
	}
	if in.Datetime != "" {
		t, err := s.parseTime("datetime", in.Datetime)
		if err != nil {
			return q, err
		}
		q.when = t
	}
	return q, nil
}

// compareBool orders false before true.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case b:
		return -1
	default:
		return 1
	}
}

// legs are the public transport rides of a journey.
func (s *Service) legs(j *navJourney) []Leg {
	out := []Leg{}
	for _, sec := range j.publicTransport() {
		di := sec.DisplayInformations
		if di == nil {
			di = &navDisplayInfo{Direction: "", Code: "", CommercialMode: "", TripShortName: "", Links: nil}
		}
		from, fromID := stopOf(sec.From)
		to, toID := stopOf(sec.To)
		out = append(out, Leg{
			Mode:      modeOf(sec.linkID("commercial_mode"), di.CommercialMode, di.Code),
			Line:      di.Code,
			LineID:    sec.linkID("line"),
			Direction: di.Direction,
			From:      from,
			FromID:    fromID,
			To:        to,
			ToID:      toID,
			Departure: s.isoNavitia(sec.DepartureDateTime),
			Arrival:   s.isoNavitia(sec.ArrivalDateTime),
			Stops:     max(len(sec.StopDateTimes)-1, 0),
		})
	}
	return out
}

// stopOf is the stop area name, without its town, and id of a section's end.
func stopOf(p *navPlace) (string, string) {
	if p == nil {
		return "", ""
	}
	if p.StopPoint != nil && p.StopPoint.StopArea != nil {
		return stripTown(p.StopPoint.StopArea.Name), p.StopPoint.StopArea.ID
	}
	return stripTown(p.Name), p.ID
}

// journeys asks Navitia for journeys. When it finds only a long walk, it
// replaces each end that is not a station by the nearest station within 500
// metres and asks once more; q holds the replaced ends.
func (s *Service) journeys(ctx context.Context, q *journeyQuery) (*journeysResponse, error) {
	resp, err := s.queryJourneys(ctx, q)
	if err != nil {
		return nil, err
	}
	if answers(resp) {
		return resp, nil
	}
	asked := *q
	var tried []string
	for _, end := range []*place{&q.from, &q.to} {
		if end.station() {
			continue
		}
		near, ok, snapErr := s.snap(ctx, *end)
		if snapErr != nil {
			return nil, snapErr
		}
		if ok {
			tried = append(tried, fmt.Sprintf("%s for %s", near.Name, end.Name))
			*end = near
		}
	}
	if len(tried) > 0 {
		resp, err = s.queryJourneys(ctx, q)
		if err != nil {
			return nil, err
		}
		if answers(resp) {
			return resp, nil
		}
	}
	message := fmt.Sprintf("no public transport route from %q to %q", asked.from.Name, asked.to.Name)
	if len(tried) > 0 {
		message += ", even from the nearest station within " + strconv.Itoa(snapDistance) + " m (" +
			strings.Join(tried, ", ") + ")"
	}
	return nil, newError(CodeNoTransitRoute, "%s: tell the user PRIM found no public transport for this trip", message)
}

// answers reports whether a response answers the trip: it has a public
// transport journey, or a walk short enough to be the answer.
func answers(resp *journeysResponse) bool {
	return firstAnswer(resp) != nil
}

// firstAnswer is the first journey that answers the trip, or nil.
func firstAnswer(resp *journeysResponse) *navJourney {
	for i := range resp.Journeys {
		if answer(&resp.Journeys[i]) {
			return &resp.Journeys[i]
		}
	}
	return nil
}

// answer reports whether a journey answers the trip: it uses public
// transport, or walks no longer than 30 minutes.
func answer(j *navJourney) bool {
	return len(j.publicTransport()) > 0 || time.Duration(j.Duration)*time.Second <= maxWalk
}

func (s *Service) queryJourneys(ctx context.Context, q *journeyQuery) (*journeysResponse, error) {
	represents := representsDeparture
	if q.arrival {
		represents = representsArrival
	}
	query := url.Values{
		"from":                {q.from.navitia},
		"to":                  {q.to.navitia},
		"datetime":            {s.navitiaTime(q.when)},
		"datetime_represents": {represents},
		"data_freshness":      {"realtime"},
		paramDisableGeoJSON:   {valueTrue},
	}
	if q.stepFree {
		query.Set("wheelchair", valueTrue)
	}
	if q.max > 0 {
		query.Set("max_nb_journeys", strconv.Itoa(q.max))
	}
	var resp journeysResponse
	err := s.prim.Navitia(ctx, "journeys", query, &resp)
	if err != nil {
		te := AsToolError(err)
		if te.Code != CodeNotFound {
			return nil, fmt.Errorf("plan journeys from %q to %q: %w", q.from.Name, q.to.Name, err)
		}
		if navitiaErrorID(err) == "no_solution" {
			return &journeysResponse{Journeys: nil, Disruptions: nil}, nil
		}
		return nil, newError(CodeNotFound, "PRIM cannot plan from %q to %q: %s", q.from.Name, q.to.Name, te.Message)
	}
	return &resp, nil
}
