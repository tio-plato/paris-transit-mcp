package transit

import (
	"cmp"
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tio-plato/paris-transit-mcp/internal/prim"
)

const (
	// MaxStopLines is how many lines of a stop next_departures queries when
	// the client names none.
	MaxStopLines = 12
	// DefaultLimit is how many departures per line and direction next_departures returns by default.
	DefaultLimit = 3
	// stale is how long after its time a departure SIRI still lists is dropped.
	stale = time.Minute
	// maxStopLinesListed bounds the lines of a stop asked of Navitia; the
	// biggest stations have about fifty.
	maxStopLinesListed = 200
	// timetableMargin is how many timetabled departures are asked per
	// departure listed, as they spread over the directions.
	timetableMargin = 4
	// minTimetable is the fewest timetabled departures asked.
	minTimetable = 10
)

// DeparturesInput is the input of next_departures.
type DeparturesInput struct {
	Stop  string   `json:"stop"`
	Lines []string `json:"lines,omitempty"`
	Limit int      `json:"limit,omitempty"`
}

// DeparturesOutput is the result of next_departures.
type DeparturesOutput struct {
	Stop         *Endpoint        `json:"stop,omitempty"`
	Lines        []LineDepartures `json:"lines,omitempty"`
	SkippedLines []LineRef        `json:"skipped_lines,omitempty"`
	Error        *ToolError       `json:"error,omitempty"`
}

// SetError sets the result's error.
func (o *DeparturesOutput) SetError(e *ToolError) { o.Error = e }

// LineDepartures are the next departures of one line in one direction.
type LineDepartures struct {
	Line      string      `json:"line"`
	Mode      string      `json:"mode,omitempty"`
	LineID    string      `json:"line_id,omitempty"`
	Direction string      `json:"direction,omitempty"`
	Realtime  bool        `json:"realtime"`
	Next      []Departure `json:"next"`
	Error     *ToolError  `json:"error,omitempty"`
}

// Departure is one departure.
type Departure struct {
	Time        string  `json:"time"`
	Minutes     int     `json:"minutes"`
	Destination string  `json:"destination"`
	Status      *string `json:"status"`
	Aimed       *string `json:"aimed"`
	Platform    *string `json:"platform"`
}

// LineRef names a line the server did not query.
type LineRef struct {
	Line   string `json:"line"`
	Mode   string `json:"mode"`
	LineID string `json:"line_id"`
}

// NextDepartures runs next_departures.
func (s *Service) NextDepartures(ctx context.Context, in DeparturesInput) (*DeparturesOutput, error) {
	out, err := s.nextDepartures(ctx, in)
	if err != nil {
		te, callErr := resultError(err)
		if callErr != nil {
			return nil, callErr
		}
		if out == nil {
			out = &DeparturesOutput{Stop: nil, Lines: nil, SkippedLines: nil, Error: nil}
		}
		out.Error = te
	}
	return out, nil
}

func (s *Service) nextDepartures(ctx context.Context, in DeparturesInput) (*DeparturesOutput, error) {
	limit := cmp.Or(in.Limit, DefaultLimit)
	stop, err := s.departureStop(ctx, in.Stop)
	if err != nil {
		return nil, err
	}
	out := &DeparturesOutput{Stop: stop.endpoint(), Lines: []LineDepartures{}, SkippedLines: nil, Error: nil}
	atStop, err := s.stopLines(ctx, stop.ID)
	if err != nil {
		return out, err
	}
	var selected []line
	if len(in.Lines) == 0 {
		selected, out.SkippedLines = defaultLines(atStop)
	} else {
		for _, name := range in.Lines {
			l, lerr := pickStopLine(atStop, name)
			if lerr != nil {
				out.Lines = append(out.Lines, failedLine(name, "", "", AsToolError(lerr)))
				continue
			}
			if !slices.ContainsFunc(selected, func(x line) bool { return x.ID == l.ID }) {
				selected = append(selected, l)
			}
		}
	}
	for _, l := range selected {
		groups, lerr := s.lineDepartures(ctx, &stop, &l, limit)
		if lerr != nil {
			if stopsCall(lerr) {
				return nil, lerr
			}
			out.Lines = append(out.Lines, failedLine(l.Code, l.Mode, l.ID, AsToolError(lerr)))
			continue
		}
		out.Lines = append(out.Lines, groups...)
	}
	return out, nil
}

func failedLine(name, mode, id string, te *ToolError) LineDepartures {
	return LineDepartures{
		Line: name, Mode: mode, LineID: id, Direction: "", Realtime: false, Next: []Departure{}, Error: te,
	}
}

// departureStop resolves the stop parameter to a stop area: a station is
// used as it is, another place stands for the nearest station.
func (s *Service) departureStop(ctx context.Context, value string) (place, error) {
	stop, err := s.resolvePlace(ctx, "stop", value)
	if err != nil {
		return place{}, err
	}
	if stop.station() {
		return s.stopArea(ctx, stop)
	}
	near, ok, err := s.snap(ctx, stop)
	if err != nil {
		return place{}, err
	}
	if !ok {
		return place{}, newError(CodeNotFound, "no station within %d m of %q", snapDistance, stop.Name)
	}
	return near, nil
}

// stopLines are the lines that call at a stop area.
func (s *Service) stopLines(ctx context.Context, stopArea string) ([]line, error) {
	var resp linesResponse
	err := s.prim.Navitia(ctx, "stop_areas/"+stopArea+"/lines", url.Values{
		paramCount: {strconv.Itoa(maxStopLinesListed)}, "depth": {"1"}, paramDisableGeoJSON: {valueTrue},
	}, &resp)
	if err != nil {
		return nil, err
	}
	lines := make([]line, 0, len(resp.Lines))
	for i := range resp.Lines {
		lines = append(lines, lineFromNavitia(&resp.Lines[i]))
	}
	return lines, nil
}

// defaultLines picks the lines to query when the client names none: up to 12,
// by mode then number; the others are skipped.
func defaultLines(atStop []line) ([]line, []LineRef) {
	sorted := slices.Clone(atStop)
	slices.SortStableFunc(sorted, func(a, b line) int {
		an, as := codeKey(a.Code)
		bn, bs := codeKey(b.Code)
		return cmp.Or(cmp.Compare(modeRank(a.Mode), modeRank(b.Mode)), cmp.Compare(an, bn), cmp.Compare(as, bs))
	})
	selected := sorted[:min(len(sorted), MaxStopLines)]
	var skipped []LineRef
	for _, l := range sorted[len(selected):] {
		skipped = append(skipped, LineRef{Line: l.label(), Mode: l.Mode, LineID: l.ID})
	}
	return selected, skipped
}

// pickStopLine resolves a line name or id among the lines of the stop.
func pickStopLine(atStop []line, name string) (line, error) {
	v := strings.TrimSpace(name)
	if lineIDPattern.MatchString(v) {
		for _, l := range atStop {
			if l.ID == v {
				return l, nil
			}
		}
		return line{}, newError(CodeNotFound, "line %q does not call at this stop", name)
	}
	l, matches := parseLineName(v).match(atStop)
	if l.ID != "" {
		return l, nil
	}
	if len(matches) == 0 {
		return line{}, newError(CodeNotFound, "line %q does not call at this stop", name)
	}
	return line{}, lineError(name, matches)
}

// lineDepartures are a line's next departures at a stop, by direction: from
// SIRI, else from the timetable. Only an error that ends the call, or a
// failed timetable request, is returned.
func (s *Service) lineDepartures(ctx context.Context, stop *place, l *line, limit int) ([]LineDepartures, error) {
	stopRef, ok1 := prim.SIRIStopRef(stop.ID)
	lineRef, ok2 := prim.SIRILineRef(l.ID)
	if ok1 && ok2 {
		sm, err := s.prim.StopMonitoring(ctx, stopRef, lineRef)
		switch {
		case err != nil && stopsCall(err):
			return nil, err
		case err == nil:
			if groups := s.realtimeGroups(sm.Visits(), l, limit); len(groups) > 0 {
				return groups, nil
			}
		}
	}
	return s.timetableGroups(ctx, stop, l, limit)
}

type groupedDeparture struct {
	at  time.Time
	dep Departure
	// terminus names the departure's terminus in a direction made of termini.
	terminus string
}

type departureGroup struct {
	key, direction string
	departures     []groupedDeparture
}

// groups collects departures by key, keeping the order keys first appear in.
type groups struct {
	order []string
	byKey map[string]*departureGroup
}

func (g *groups) add(key, direction string, d groupedDeparture) {
	if g.byKey == nil {
		g.byKey = map[string]*departureGroup{}
	}
	grp := g.byKey[key]
	if grp == nil {
		grp = &departureGroup{key: key, direction: direction, departures: nil}
		g.byKey[key] = grp
		g.order = append(g.order, key)
	}
	grp.departures = append(grp.departures, d)
}

// result turns the groups into one result per direction, each with at most
// limit departures, the direction with the next departure first. A group
// without a direction name is named by the termini of the departures listed.
func (g *groups) result(l *line, realtime bool, limit int) []LineDepartures {
	var out []LineDepartures
	var firsts []time.Time
	for _, key := range g.order {
		grp := g.byKey[key]
		slices.SortStableFunc(grp.departures, func(a, b groupedDeparture) int { return a.at.Compare(b.at) })
		next := make([]Departure, 0, limit)
		var termini []string
		for _, d := range grp.departures[:min(len(grp.departures), limit)] {
			next = append(next, d.dep)
			if d.terminus != "" && !slices.Contains(termini, d.terminus) {
				termini = append(termini, d.terminus)
			}
		}
		direction := grp.direction
		if direction == "" {
			direction = strings.Join(termini, " / ")
		}
		out = append(out, LineDepartures{
			Line: l.Code, Mode: l.Mode, LineID: l.ID, Direction: direction, Realtime: realtime, Next: next, Error: nil,
		})
		firsts = append(firsts, grp.departures[0].at)
	}
	idx := make([]int, len(out))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int { return firsts[a].Compare(firsts[b]) })
	sorted := make([]LineDepartures, 0, len(out))
	for _, i := range idx {
		sorted = append(sorted, out[i])
	}
	return sorted
}

// realtimeGroups groups SIRI departures by DirectionRef, else by destination.
func (s *Service) realtimeGroups(visits []visit, l *line, limit int) []LineDepartures {
	var g groups
	now := s.now()
	for i := range visits {
		v := &visits[i]
		at, ok := v.Departure()
		if !ok || at.Before(now.Add(-stale)) {
			continue
		}
		j := v.MonitoredVehicleJourney
		call := j.MonitoredCall
		d := Departure{
			Time:        s.iso(at),
			Minutes:     s.minutesUntil(at),
			Destination: v.Destination(),
			Status:      nonEmpty(call.DepartureStatus),
			Aimed:       nil,
			Platform:    nil,
		}
		if call.AimedDepartureTime != nil {
			aimed := s.iso(*call.AimedDepartureTime)
			d.Aimed = &aimed
		}
		if p := call.DeparturePlatformName.Value; p != "" && p != "unknown" {
			d.Platform = &p
		}
		key, direction := "dest:"+d.Destination, d.Destination
		if ref := j.DirectionRef.Value; ref != "" {
			key, direction = "ref:"+ref, ""
			if len(j.DirectionName) > 0 {
				direction = j.DirectionName[0].Value
			}
		}
		g.add(key, direction, groupedDeparture{at: at, dep: d, terminus: d.Destination})
	}
	return g.result(l, true, limit)
}

// timetableGroups groups timetabled departures by route direction.
func (s *Service) timetableGroups(ctx context.Context, stop *place, l *line, limit int) ([]LineDepartures, error) {
	var resp departuresResponse
	err := s.prim.Navitia(ctx, "stop_areas/"+stop.ID+"/departures", url.Values{
		"data_freshness":    {"base_schedule"},
		"filter":            {"line.id=" + l.ID},
		"datetime":          {s.navitiaTime(s.now())},
		paramCount:          {strconv.Itoa(max(minTimetable, timetableMargin*limit))},
		paramDisableGeoJSON: {valueTrue},
	}, &resp)
	if err != nil && !noDeparture(err) {
		return nil, err
	}
	var g groups
	for _, d := range resp.Departures {
		at, ok := s.parseNavitia(d.StopDateTime.DepartureDateTime)
		if !ok {
			continue
		}
		key := cmp.Or(d.Route.DirectionType, d.Route.Direction.ID, d.Route.ID)
		dest := d.DisplayInformations.Direction
		g.add(key, "", groupedDeparture{at: at, dep: Departure{
			Time: s.iso(at), Minutes: s.minutesUntil(at), Destination: dest, Status: nil, Aimed: nil, Platform: nil,
		}, terminus: d.Route.Direction.Name})
	}
	result := g.result(l, false, limit)
	if len(result) == 0 {
		result = []LineDepartures{{
			Line: l.Code, Mode: l.Mode, LineID: l.ID, Direction: "", Realtime: false, Next: []Departure{}, Error: nil,
		}}
	}
	return result, nil
}

// noDeparture reports whether err is Navitia saying the stop has no departure
// to give, as opposed to a failure.
func noDeparture(err error) bool {
	switch navitiaErrorID(err) {
	case "no_departure_this_day", "no_active_circulation_this_day", "terminus", "partial_terminus",
		"active_disruption":
		return true
	default:
		return false
	}
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
