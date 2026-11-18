package transit

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"
)

// MaxDestinations is the most destinations rank_destinations takes.
const MaxDestinations = 20

// RankInput is the input of rank_destinations.
type RankInput struct {
	Origin       string             `json:"origin"`
	Destinations []DestinationInput `json:"destinations"`
	DepartAt     string             `json:"depart_at,omitempty"`
}

// DestinationInput is one destination to rank.
type DestinationInput struct {
	Label    string `json:"label"`
	Place    string `json:"place"`
	ArriveBy string `json:"arrive_by,omitempty"`
}

// RankOutput is the result of rank_destinations.
type RankOutput struct {
	Origin *Endpoint  `json:"origin,omitempty"`
	Ranked []Ranked   `json:"ranked,omitempty"`
	Failed []Failed   `json:"failed,omitempty"`
	Error  *ToolError `json:"error,omitempty"`
}

// SetError sets the result's error.
func (o *RankOutput) SetError(e *ToolError) { o.Error = e }

// Ranked is a destination with its door-to-door trip.
type Ranked struct {
	Label         string   `json:"label"`
	Resolved      Resolved `json:"resolved"`
	DurationMin   int      `json:"duration_min"`
	Transfers     int      `json:"transfers"`
	WalkingMin    int      `json:"walking_min"`
	Departure     string   `json:"departure"`
	Arrival       string   `json:"arrival"`
	Lines         []string `json:"lines"`
	CitymapperURL string   `json:"citymapper_url"`
}

// Failed is a destination the server could not rank.
type Failed struct {
	Label string    `json:"label"`
	Error ToolError `json:"error"`
}

// RankDestinations runs rank_destinations.
func (s *Service) RankDestinations(ctx context.Context, in RankInput) (*RankOutput, error) {
	out, err := s.rankDestinations(ctx, in)
	if err != nil {
		te, callErr := resultError(err)
		if callErr != nil {
			return nil, callErr
		}
		return &RankOutput{Origin: nil, Ranked: nil, Failed: nil, Error: te}, nil
	}
	return out, nil
}

func (s *Service) rankDestinations(ctx context.Context, in RankInput) (*RankOutput, error) {
	if len(in.Destinations) == 0 || len(in.Destinations) > MaxDestinations {
		return nil, newError(CodeBadRequest, "destinations has %d items; it takes 1 to %d",
			len(in.Destinations), MaxDestinations)
	}
	depart := s.now()
	if in.DepartAt != "" {
		t, err := s.parseTime("depart_at", in.DepartAt)
		if err != nil {
			return nil, err
		}
		depart = t
	}
	// Every time is checked before the first request, so a wrong one costs no quota.
	arriveBy := make([]time.Time, len(in.Destinations))
	for i, d := range in.Destinations {
		if d.ArriveBy == "" {
			continue
		}
		t, err := s.parseTime(fmt.Sprintf("destinations[%d].arrive_by", i), d.ArriveBy)
		if err != nil {
			return nil, err
		}
		arriveBy[i] = t
	}
	asked, err := s.resolvePlace(ctx, "origin", in.Origin)
	if err != nil {
		return nil, err
	}
	origin := asked
	out := &RankOutput{Origin: origin.endpoint(), Ranked: []Ranked{}, Failed: []Failed{}, Error: nil}
	for i, d := range in.Destinations {
		r, from, oneErr := s.rankOne(ctx, trip{asked: asked, from: origin}, d, depart, arriveBy[i])
		// An origin replaced by a station for one destination stays replaced
		// for the next ones, and the result says so.
		if from.SnappedFrom != nil && origin.SnappedFrom == nil {
			origin = from
			out.Origin = origin.endpoint()
		}
		if oneErr != nil {
			if stopsCall(oneErr) {
				return nil, oneErr
			}
			out.Failed = append(out.Failed, Failed{Label: d.Label, Error: *AsToolError(oneErr)})
			continue
		}
		out.Ranked = append(out.Ranked, *r)
	}
	slices.SortStableFunc(out.Ranked, func(a, b Ranked) int {
		return cmp.Or(
			cmp.Compare(a.DurationMin, b.DurationMin),
			cmp.Compare(a.Transfers, b.Transfers),
			cmp.Compare(a.WalkingMin, b.WalkingMin),
		)
	})
	return out, nil
}

// trip is the origin of a ranking: as asked, and as sent to PRIM, which may
// be a station near it.
type trip struct {
	asked, from place
}

// rankOne plans the trip to one destination: PRIM's first journey, by
// timetable, without realtime. It also returns the origin PRIM was asked
// from.
func (s *Service) rankOne(
	ctx context.Context, origin trip, d DestinationInput, depart, arriveBy time.Time,
) (*Ranked, place, error) {
	to, err := s.resolvePlace(ctx, "place", d.Place)
	if err != nil {
		return nil, origin.from, err
	}
	q := journeyQuery{from: origin.from, to: to, when: depart, arrival: false, stepFree: false, max: 1}
	if !arriveBy.IsZero() {
		q.when, q.arrival = arriveBy, true
	}
	resp, err := s.journeys(ctx, &q)
	if err != nil {
		return nil, q.from, err
	}
	j := firstAnswer(resp)
	lines := []string{}
	for _, sec := range j.publicTransport() {
		if di := sec.DisplayInformations; di != nil {
			lines = append(lines, label(modeOf(sec.linkID("commercial_mode"), di.CommercialMode, di.Code), di.Code))
		}
	}
	var arrival time.Time
	if q.arrival {
		arrival = q.when
	}
	return &Ranked{
		Label:         d.Label,
		Resolved:      q.to.Resolved,
		DurationMin:   roundMinutes(j.Duration),
		Transfers:     j.NbTransfers,
		WalkingMin:    roundMinutes(j.Durations.Walking),
		Departure:     s.isoNavitia(j.DepartureDateTime),
		Arrival:       s.isoNavitia(j.ArrivalDateTime),
		Lines:         lines,
		CitymapperURL: s.citymapperURL(&origin.asked.Resolved, &to.Resolved, arrival),
	}, q.from, nil
}
