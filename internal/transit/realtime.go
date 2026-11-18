package transit

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/tio-plato/paris-transit-mcp/internal/prim"
)

const (
	// realtimeHorizon: SIRI covers about 3 hours, and later trains are mostly
	// timetable anyway.
	realtimeHorizon = 90 * time.Minute
	// catchable: a train leaving up to 2 minutes early can still be caught.
	catchable       = 2 * time.Minute
	directionTrains = 3

	statusCancelled = "cancelled"
	matchTrip       = "trip"
	matchDirection  = "direction"
)

type visit = prim.Visit

type FirstLegRealtime struct {
	// Match is "trip" for this very train, "direction" for the next trains
	// to the same terminus.
	Match    string      `json:"match"`
	Expected string      `json:"expected,omitempty"`
	DelayMin *int        `json:"delay_min,omitempty"`
	Status   string      `json:"status,omitempty"`
	Next     []NextTrain `json:"next,omitempty"`
}

type NextTrain struct {
	Time    string `json:"time"`
	Minutes int    `json:"minutes"`
	Status  string `json:"status"`
}

// realtimeLookup asks SIRI at most once per stop and line in a call. After
// an auth or quota error it stops asking: SIRI has its own quota, so the
// rest of the call goes on with timetable times.
type realtimeLookup struct {
	s       *Service
	cache   map[string]lookup
	failed  error // the first failure, for realtime_unavailable
	stopped bool
}

type lookup struct {
	visits []visit
	err    error
}

func newRealtimeLookup(s *Service) *realtimeLookup {
	return &realtimeLookup{s: s, cache: map[string]lookup{}, failed: nil, stopped: false}
}

func (r *realtimeLookup) visits(ctx context.Context, stopRef, lineRef string) ([]visit, error) {
	key := stopRef + " " + lineRef
	if l, ok := r.cache[key]; ok {
		return l.visits, l.err
	}
	if r.stopped {
		return nil, r.failed
	}
	var l lookup
	sm, err := r.s.prim.StopMonitoring(ctx, stopRef, lineRef)
	if err != nil {
		l.err = fmt.Errorf("ask SIRI for line %s at %s: %w", lineRef, stopRef, err)
		if r.failed == nil {
			r.failed = l.err
		}
		r.stopped = stopsCall(err)
	} else {
		l.visits = sm.Visits()
	}
	r.cache[key] = l
	return l.visits, l.err
}

// unavailable is the realtime_unavailable notice, or nil when SIRI answered.
func (r *realtimeLookup) unavailable() *ToolError {
	if r.failed == nil {
		return nil
	}
	te := AsToolError(r.failed)
	return &ToolError{
		Code:       te.Code,
		Message:    "live times are missing, timetable only: " + te.Message,
		Candidates: nil,
		cause:      r.failed,
	}
}

// firstLeg matches realtime to the first ride if it leaves within 90 minutes.
// cancelled is true when the matched train is cancelled.
func (r *realtimeLookup) firstLeg(ctx context.Context, j *navJourney) (*FirstLegRealtime, bool) {
	rides := j.publicTransport()
	if len(rides) == 0 {
		return nil, false
	}
	sec := rides[0]
	depValue := sec.BaseDepartureDateTime
	if depValue == "" {
		depValue = sec.DepartureDateTime
	}
	dep, ok := r.s.parseNavitia(depValue)
	if !ok || dep.Sub(r.s.now()) > realtimeHorizon || sec.DisplayInformations == nil {
		return nil, false
	}
	_, stopArea := stopOf(sec.From)
	stopRef, ok1 := prim.SIRIStopRef(stopArea)
	lineRef, ok2 := prim.SIRILineRef(sec.linkID("line"))
	if !ok1 || !ok2 {
		return nil, false
	}
	visits, err := r.visits(ctx, stopRef, lineRef)
	if err != nil {
		return nil, false
	}
	di := sec.DisplayInformations
	if m := r.byTrip(visits, di.TripShortName, dep); m != nil {
		return m, m.Status == statusCancelled
	}
	return r.byDirection(visits, di.Direction, dep), false
}

// byTrip wants both the train number and the timetabled minute.
func (r *realtimeLookup) byTrip(visits []visit, trip string, dep time.Time) *FirstLegRealtime {
	if trip == "" {
		return nil
	}
	for i := range visits {
		v := &visits[i]
		call := v.MonitoredVehicleJourney.MonitoredCall
		if !v.HasTrainNumber(trip) || call.AimedDepartureTime == nil ||
			!call.AimedDepartureTime.Truncate(time.Minute).Equal(dep.Truncate(time.Minute)) {
			continue
		}
		m := &FirstLegRealtime{
			Match: matchTrip, Expected: "", DelayMin: nil, Status: call.DepartureStatus, Next: nil,
		}
		if call.ExpectedDepartureTime != nil {
			m.Expected = r.s.iso(*call.ExpectedDepartureTime)
			delay := int(call.ExpectedDepartureTime.Sub(*call.AimedDepartureTime).Round(time.Minute) / time.Minute)
			m.DelayMin = &delay
		}
		return m
	}
	return nil
}

func (r *realtimeLookup) byDirection(visits []visit, direction string, dep time.Time) *FirstLegRealtime {
	type train struct {
		at     time.Time
		status string
	}
	var trains []train
	for i := range visits {
		v := &visits[i]
		at, ok := v.Departure()
		if !ok || !sameStop(v.Destination(), direction) || at.Before(dep.Add(-catchable)) {
			continue
		}
		trains = append(trains, train{at: at, status: v.MonitoredVehicleJourney.MonitoredCall.DepartureStatus})
	}
	if len(trains) == 0 {
		return nil
	}
	slices.SortStableFunc(trains, func(a, b train) int { return a.at.Compare(b.at) })
	m := &FirstLegRealtime{Match: matchDirection, Expected: "", DelayMin: nil, Status: "", Next: nil}
	for _, t := range trains[:min(len(trains), directionTrains)] {
		m.Next = append(m.Next, NextTrain{Time: r.s.iso(t.at), Minutes: r.s.minutesUntil(t.at), Status: t.status})
	}
	return m
}
