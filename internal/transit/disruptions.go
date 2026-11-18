package transit

import (
	"html"
	"regexp"
	"slices"
	"strings"
	"time"
)

// equipmentTag: PRIM gives broken lifts the same severity as line faults, so
// only the tag tells them apart. Seen so far: Ascenseur, and Actualité (news).
func equipmentTag(tag string) bool {
	switch tag {
	case "Ascenseur":
		return true
	default:
		return false
	}
}

func (d *navDisruption) equipment() bool {
	return slices.ContainsFunc(d.Tags, equipmentTag)
}

// silent: a realtime record of a trip, with nothing to say.
func (d *navDisruption) silent() bool {
	return len(d.Messages) == 0 && d.Severity.Effect == "UNKNOWN_EFFECT"
}

func (d *navDisruption) title() string {
	for _, m := range d.Messages {
		if slices.Contains(m.Channel.Types, "title") {
			return m.plainText()
		}
	}
	return d.Severity.Name
}

func (d *navDisruption) text() string {
	for _, m := range d.Messages {
		if slices.Contains(m.Channel.Types, "web") {
			return m.plainText()
		}
	}
	for _, m := range d.Messages {
		if !slices.Contains(m.Channel.Types, "title") {
			return m.plainText()
		}
	}
	return ""
}

func (d *navDisruption) stopAreas() []string {
	var out []string
	for _, o := range d.ImpactedObjects {
		switch o.PtObject.EmbeddedType {
		case typeStopArea:
			out = append(out, o.PtObject.ID)
		case typeStopPoint:
			if sp := o.PtObject.StopPoint; sp != nil && sp.StopArea != nil {
				out = append(out, sp.StopArea.ID)
			}
		}
	}
	return out
}

var (
	lineBreakTags = regexp.MustCompile(`(?i)<\s*(br|/p|/li|/div)\s*/?>`)
	tags          = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
	blanks        = regexp.MustCompile(`[ \t\r\f\v\x{a0}]+`)
	blankLines    = regexp.MustCompile(`\s*\n\s*`)
)

// plainText strips tags from HTML messages only: titles like
// "Châtelet <-> Mitry" are plain text.
func (m *navMessage) plainText() string {
	s := m.Text
	if m.Channel.ContentType == "text/html" {
		s = lineBreakTags.ReplaceAllString(s, "\n")
		s = tags.ReplaceAllString(s, "")
		s = html.UnescapeString(s)
	}
	s = blanks.ReplaceAllString(s, " ")
	s = blankLines.ReplaceAllString(s, "\n")
	return strings.TrimSpace(s)
}

type period struct {
	begin, end time.Time
}

func (s *Service) periods(d *navDisruption) []period {
	var out []period
	for _, p := range d.ApplicationPeriods {
		b, ok1 := s.parseNavitia(p.Begin)
		e, ok2 := s.parseNavitia(p.End)
		if ok1 && ok2 {
			out = append(out, period{begin: b, end: e})
		}
	}
	slices.SortFunc(out, func(a, b period) int { return a.begin.Compare(b.begin) })
	return out
}

// periodAt returns the period in force at t, else the next one.
func (s *Service) periodAt(d *navDisruption, t time.Time) (period, bool) {
	for _, p := range s.periods(d) {
		if p.end.After(t) {
			return p, true
		}
	}
	return period{}, false
}

type Disruptions struct {
	Service   []ServiceDisruption   `json:"service"`
	Equipment []EquipmentDisruption `json:"equipment"`
}

type ServiceDisruption struct {
	Lines  []string `json:"lines"`
	Status string   `json:"status"`
	Title  string   `json:"title"`
	Text   string   `json:"text"`
	From   string   `json:"from"`
	Until  string   `json:"until"`
}

type EquipmentDisruption struct {
	Stop  string `json:"stop"`
	Title string `json:"title"`
	Text  string `json:"text"`
	Until string `json:"until"`
}

// journeyDisruptions keeps equipment faults only where the journey boards,
// changes or alights.
func (s *Service) journeyDisruptions(j *navJourney, all map[string]*navDisruption) Disruptions {
	out := Disruptions{Service: []ServiceDisruption{}, Equipment: []EquipmentDisruption{}}
	stops := boardingStops(j)
	at, arrival := s.journeyWindow(j)
	// PRIM republishes the same lift fault with different end dates.
	type lift struct {
		index int
		end   time.Time
	}
	lifts := map[string]lift{}
	for _, ld := range linkedDisruptions(j, all) {
		d := ld.d
		p, dated, listed := s.periodFor(d, at, arrival)
		if !listed {
			continue
		}
		var from, until string
		if dated {
			from, until = s.iso(p.begin), s.iso(p.end)
		}
		switch {
		case d.equipment():
			stop, found := firstStop(d, stops)
			if !found {
				continue
			}
			e := EquipmentDisruption{Stop: stop, Title: d.title(), Text: d.text(), Until: until}
			key := e.Stop + "\x00" + e.Title + "\x00" + e.Text
			if l, dup := lifts[key]; dup {
				if p.end.After(l.end) {
					out.Equipment[l.index].Until = e.Until
					lifts[key] = lift{index: l.index, end: p.end}
				}
				continue
			}
			lifts[key] = lift{index: len(out.Equipment), end: p.end}
			out.Equipment = append(out.Equipment, e)
		case !d.silent():
			out.Service = append(out.Service, ServiceDisruption{
				Lines: ld.lines, Status: d.Status, Title: d.title(), Text: d.text(), From: from, Until: until,
			})
		}
	}
	return out
}

func (s *Service) journeyWindow(j *navJourney) (time.Time, time.Time) {
	at, ok := s.parseNavitia(j.DepartureDateTime)
	if !ok {
		at = s.now()
	}
	arrival, ok := s.parseNavitia(j.ArrivalDateTime)
	if !ok || arrival.Before(at) {
		arrival = at
	}
	return at, arrival
}

// periodFor picks the period relevant to the journey, not to now. listed is
// false if d ends before the journey or starts after it; with no readable
// period it's listed without dates.
func (s *Service) periodFor(d *navDisruption, at, arrival time.Time) (period, bool, bool) {
	if len(s.periods(d)) == 0 {
		return period{}, false, true
	}
	p, ok := s.periodAt(d, at)
	if !ok || p.begin.After(arrival) {
		return period{}, false, false
	}
	return p, true, true
}

func boardingStops(j *navJourney) map[string]string {
	stops := map[string]string{}
	for _, sec := range j.publicTransport() {
		for _, end := range []*navPlace{sec.From, sec.To} {
			if end != nil && end.StopPoint != nil && end.StopPoint.StopArea != nil {
				stops[end.StopPoint.StopArea.ID] = stripTown(end.StopPoint.StopArea.Name)
			}
		}
	}
	return stops
}

func firstStop(d *navDisruption, stops map[string]string) (string, bool) {
	for _, sa := range d.stopAreas() {
		if name, ok := stops[sa]; ok {
			return name, true
		}
	}
	return "", false
}

type linkedDisruption struct {
	d     *navDisruption
	lines []string
}

func linkedDisruptions(j *navJourney, all map[string]*navDisruption) []*linkedDisruption {
	var out []*linkedDisruption
	byID := map[string]*linkedDisruption{}
	for _, sec := range j.publicTransport() {
		di := sec.DisplayInformations
		if di == nil {
			continue
		}
		for _, l := range di.Links {
			d := all[l.ID]
			if l.Type != "disruption" || d == nil {
				continue
			}
			ld := byID[d.ID]
			if ld == nil {
				ld = &linkedDisruption{d: d, lines: []string{}}
				byID[d.ID] = ld
				out = append(out, ld)
			}
			if di.Code != "" && !slices.Contains(ld.lines, di.Code) {
				ld.lines = append(ld.lines, di.Code)
			}
		}
	}
	return out
}
