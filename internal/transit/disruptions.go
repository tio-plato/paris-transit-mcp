package transit

import (
	"html"
	"regexp"
	"slices"
	"strings"
	"time"
)

// equipmentTag reports whether a Navitia tag marks an equipment fault, such
// as a broken lift. PRIM publishes them as disruptions with the same severity
// as a line fault, so the tag is what tells them apart. Tags seen in PRIM
// responses on 2026-11-18: Ascenseur (equipment) and Actualité (news, a
// service message).
func equipmentTag(tag string) bool {
	switch tag {
	case "Ascenseur":
		return true
	default:
		return false
	}
}

// equipment reports whether a disruption is an equipment fault.
func (d *navDisruption) equipment() bool {
	return slices.ContainsFunc(d.Tags, equipmentTag)
}

// silent reports whether a disruption says nothing: a realtime feed's record
// of a trip, with no message and an unknown effect.
func (d *navDisruption) silent() bool {
	return len(d.Messages) == 0 && d.Severity.Effect == "UNKNOWN_EFFECT"
}

// title is the disruption's title message, else its severity.
func (d *navDisruption) title() string {
	for _, m := range d.Messages {
		if slices.Contains(m.Channel.Types, "title") {
			return m.plainText()
		}
	}
	return d.Severity.Name
}

// text is the disruption's web message, else its first message that is not
// the title.
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

// stopAreas are the ids of the stop areas the disruption impacts.
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

// plainText is the message as plain text. Only an HTML message loses its
// tags: a plain text title such as "Châtelet <-> Mitry" keeps its arrow.
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

// period is an application period in Paris time.
type period struct {
	begin, end time.Time
}

// periods returns the disruption's readable periods, sorted by start.
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

// periodAt returns the period in force at t, else the next one; ok is false
// when every period has ended by t.
func (s *Service) periodAt(d *navDisruption, t time.Time) (period, bool) {
	for _, p := range s.periods(d) {
		if p.end.After(t) {
			return p, true
		}
	}
	return period{}, false
}

// Disruptions are the disruptions of a journey.
type Disruptions struct {
	Service   []ServiceDisruption   `json:"service"`
	Equipment []EquipmentDisruption `json:"equipment"`
}

// ServiceDisruption is a disruption of the service: a fault, works, a strike.
type ServiceDisruption struct {
	Lines  []string `json:"lines"`
	Status string   `json:"status"`
	Title  string   `json:"title"`
	Text   string   `json:"text"`
	From   string   `json:"from"`
	Until  string   `json:"until"`
}

// EquipmentDisruption is an equipment fault, such as a broken lift, at a stop.
type EquipmentDisruption struct {
	Stop  string `json:"stop"`
	Title string `json:"title"`
	Text  string `json:"text"`
	Until string `json:"until"`
}

// journeyDisruptions sorts the disruptions linked to a journey's sections into
// service disruptions and equipment faults at the stops where the journey
// boards, changes or alights.
func (s *Service) journeyDisruptions(j *navJourney, all map[string]*navDisruption) Disruptions {
	out := Disruptions{Service: []ServiceDisruption{}, Equipment: []EquipmentDisruption{}}
	stops := boardingStops(j)
	// The periods are those of the journey's time, not of now: nightly works
	// tonight say nothing of a trip next week.
	at, readable := s.parseNavitia(j.DepartureDateTime)
	if !readable {
		at = s.now()
	}
	// PRIM republishes the same lift fault many times, each with its own end:
	// it is listed once, until the latest end.
	lifts := map[string]int{}
	var liftEnds []time.Time
	for _, ld := range linkedDisruptions(j, all) {
		d := ld.d
		p, ok := s.periodAt(d, at)
		var from, until string
		if ok {
			from, until = s.iso(p.begin), s.iso(p.end)
		}
		switch {
		case d.equipment():
			stop, at := firstStop(d, stops)
			if !at {
				continue
			}
			e := EquipmentDisruption{Stop: stop, Title: d.title(), Text: d.text(), Until: until}
			key := e.Stop + "\x00" + e.Title + "\x00" + e.Text
			if i, dup := lifts[key]; dup {
				if ok && p.end.After(liftEnds[i]) {
					out.Equipment[i].Until, liftEnds[i] = until, p.end
				}
				continue
			}
			lifts[key] = len(out.Equipment)
			out.Equipment = append(out.Equipment, e)
			liftEnds = append(liftEnds, p.end)
		case !d.silent():
			out.Service = append(out.Service, ServiceDisruption{
				Lines: ld.lines, Status: d.Status, Title: d.title(), Text: d.text(), From: from, Until: until,
			})
		}
	}
	return out
}

// boardingStops maps the stop areas where a journey boards, changes or
// alights to their names, without the town.
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

// firstStop is the first of the stops a disruption impacts that is in stops.
func firstStop(d *navDisruption, stops map[string]string) (string, bool) {
	for _, sa := range d.stopAreas() {
		if name, ok := stops[sa]; ok {
			return name, true
		}
	}
	return "", false
}

// linkedDisruption is a disruption linked to a journey, with the codes of the
// lines whose sections link it.
type linkedDisruption struct {
	d     *navDisruption
	lines []string
}

// linkedDisruptions are the disruptions the journey's public transport
// sections link to, each once, in the order they first appear.
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
