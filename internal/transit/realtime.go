package transit

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/tio-plato/paris-transit-mcp/internal/prim"
)

const (
	// realtimeHorizon is how far ahead a first leg may leave and still get
	// realtime data: SIRI covers about the next three hours, and a later
	// train's realtime would mostly be its timetable.
	realtimeHorizon = 90 * time.Minute
	// catchable is how long before the timetabled departure a train may leave
	// and still be caught on this journey.
	catchable = 2 * time.Minute
	// directionTrains is how many trains a match by direction lists.
	directionTrains = 3

	statusCancelled = "cancelled"
	matchTrip       = "trip"
	matchDirection  = "direction"
)

type visit = prim.Visit

// FirstLegRealtime is the realtime data matched to a journey's first ride.
type FirstLegRealtime struct {
	// Match is trip when SIRI has this very train, direction when it lists
	// trains of the line towards the same terminus.
	Match    string      `json:"match"`
	Expected string      `json:"expected,omitempty"`
	DelayMin *int        `json:"delay_min,omitempty"`
	Status   string      `json:"status,omitempty"`
	Next     []NextTrain `json:"next,omitempty"`
}

// NextTrain is a train of a match by direction.
type NextTrain struct {
	Time    string `json:"time"`
	Minutes int    `json:"minutes"`
	Status  string `json:"status"`
}

// realtimeLookup asks SIRI once per stop and line within a call, whatever
// the answer: a failure is not retried for the next journey.
type realtimeLookup struct {
	s     *Service
	cache map[string]lookup
}

type lookup struct {
	visits []visit
	err    error
}

func (r *realtimeLookup) visits(ctx context.Context, stopRef, lineRef string) ([]visit, error) {
	key := stopRef + " " + lineRef
	if l, ok := r.cache[key]; ok {
		return l.visits, l.err
	}
	var l lookup
	sm, err := r.s.prim.StopMonitoring(ctx, stopRef, lineRef)
	if err != nil {
		l.err = fmt.Errorf("ask SIRI for line %s at %s: %w", lineRef, stopRef, err)
	} else {
		l.visits = sm.Visits()
	}
	r.cache[key] = l
	return l.visits, l.err
}

// firstLeg matches realtime data to the journey's first public transport
// ride when it leaves within 90 minutes. cancelled is true when the matched
// train is cancelled. An error is returned only when it ends the call; a
// failed SIRI request otherwise just leaves the journey without realtime.
func (r *realtimeLookup) firstLeg(ctx context.Context, j *navJourney) (*FirstLegRealtime, bool, error) {
	rides := j.publicTransport()
	if len(rides) == 0 {
		return nil, false, nil
	}
	sec := rides[0]
	depValue := sec.BaseDepartureDateTime
	if depValue == "" {
		depValue = sec.DepartureDateTime
	}
	dep, ok := r.s.parseNavitia(depValue)
	if !ok || dep.Sub(r.s.now()) > realtimeHorizon || sec.DisplayInformations == nil {
		return nil, false, nil
	}
	_, stopArea := stopOf(sec.From)
	stopRef, ok1 := prim.SIRIStopRef(stopArea)
	lineRef, ok2 := prim.SIRILineRef(sec.linkID("line"))
	if !ok1 || !ok2 {
		return nil, false, nil
	}
	visits, err := r.visits(ctx, stopRef, lineRef)
	if err != nil {
		if stopsCall(err) {
			return nil, false, err
		}
		return nil, false, nil
	}
	di := sec.DisplayInformations
	if m := r.byTrip(visits, di.TripShortName, dep); m != nil {
		return m, m.Status == statusCancelled, nil
	}
	return r.byDirection(visits, di.Direction, dep), false, nil
}

// byTrip finds the train whose number is trip and whose aimed departure is
// the timetabled one, to the minute.
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

// byDirection lists the next trains towards the leg's terminus that leave no
// earlier than two minutes before the timetabled departure.
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
