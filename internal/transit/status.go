package transit

import (
	"context"
	"slices"
)

const (
	// MaxLines is the most lines next_departures and line_status take.
	MaxLines = 10
	// DefaultDays is how many days ahead line_status looks for works by default.
	DefaultDays = 7
	// maxPeriods is how many periods line_status lists per disruption.
	maxPeriods = 5
)

// StatusInput is the input of line_status.
type StatusInput struct {
	Lines []string `json:"lines"`
	Days  int      `json:"days,omitempty"`
}

// StatusOutput is the result of line_status.
type StatusOutput struct {
	Lines []LineStatus `json:"lines,omitempty"`
	Error *ToolError   `json:"error,omitempty"`
}

// SetError sets the result's error.
func (o *StatusOutput) SetError(e *ToolError) { o.Error = e }

// LineStatus is the service disruptions of one line.
type LineStatus struct {
	Line     string     `json:"line"`
	LineID   string     `json:"line_id,omitempty"`
	Active   []Report   `json:"active"`
	Upcoming []Report   `json:"upcoming"`
	Error    *ToolError `json:"error,omitempty"`
}

// Report is one service disruption of a line.
type Report struct {
	Title   string   `json:"title"`
	Text    string   `json:"text"`
	Periods []Period `json:"periods"`
}

// Period is when a disruption applies.
type Period struct {
	From  string `json:"from"`
	Until string `json:"until"`
}

// LineStatus runs line_status.
func (s *Service) LineStatus(ctx context.Context, in StatusInput) (*StatusOutput, error) {
	days := in.Days
	if days == 0 {
		days = DefaultDays
	}
	out := &StatusOutput{Lines: []LineStatus{}, Error: nil}
	failed := 0
	var lastErr error
	for _, name := range in.Lines {
		ls, err := s.lineStatus(ctx, name, days)
		if err != nil {
			if stopsCall(err) {
				return nil, err
			}
			te := AsToolError(err)
			if te.Code == CodeUpstream {
				failed++
				lastErr = err
			}
			out.Lines = append(out.Lines, LineStatus{
				Line: name, LineID: "", Active: []Report{}, Upcoming: []Report{}, Error: te,
			})
			continue
		}
		out.Lines = append(out.Lines, *ls)
	}
	if failed == len(in.Lines) {
		return nil, lastErr
	}
	return out, nil
}

func (s *Service) lineStatus(ctx context.Context, name string, days int) (*LineStatus, error) {
	l, err := s.resolveLine(ctx, name)
	if err != nil {
		return nil, err
	}
	var resp lineReportsResponse
	// A line without disruption answers 200; a 404 is never "all clear".
	err = s.prim.Navitia(ctx, "line_reports/lines/"+l.ID+"/line_reports", nil, &resp)
	if err != nil {
		return nil, failure(err, "read the line reports of %s", l.ID)
	}
	out := &LineStatus{Line: l.label(), LineID: l.ID, Active: []Report{}, Upcoming: []Report{}, Error: nil}
	now := s.now()
	horizon := now.AddDate(0, 0, days)
	for _, m := range s.mergeReports(resp.Disruptions) {
		var active, upcoming bool
		var listed []period
		for _, p := range m.periods {
			if !p.end.After(now) {
				continue
			}
			active = active || !p.begin.After(now)
			upcoming = upcoming || !p.begin.After(horizon)
			if len(listed) < maxPeriods {
				listed = append(listed, p)
			}
		}
		r := Report{Title: m.title, Text: m.text, Periods: s.reportPeriods(listed)}
		switch {
		case active:
			out.Active = append(out.Active, r)
		case upcoming:
			out.Upcoming = append(out.Upcoming, r)
		}
	}
	return out, nil
}

// mergedReport is a service disruption with all its periods, sorted by start.
type mergedReport struct {
	title, text string
	periods     []period
}

// mergeReports keeps the service disruptions. PRIM may publish one disruption
// as several impacts with the same title and text, say one per section of the
// line: they are merged into one, with their periods together.
func (s *Service) mergeReports(disruptions []navDisruption) []*mergedReport {
	var out []*mergedReport
	byKey := map[string]*mergedReport{}
	for i := range disruptions {
		d := &disruptions[i]
		if d.equipment() || d.silent() {
			continue
		}
		title, text := d.title(), d.text()
		key := title + "\x00" + text
		m := byKey[key]
		if m == nil {
			m = &mergedReport{title: title, text: text, periods: nil}
			byKey[key] = m
			out = append(out, m)
		}
		for _, p := range s.periods(d) {
			if !slices.Contains(m.periods, p) {
				m.periods = append(m.periods, p)
			}
		}
	}
	for _, m := range out {
		slices.SortFunc(m.periods, func(a, b period) int { return a.begin.Compare(b.begin) })
	}
	return out
}

func (s *Service) reportPeriods(ps []period) []Period {
	out := make([]Period, 0, len(ps))
	for _, p := range ps {
		out = append(out, Period{From: s.iso(p.begin), Until: s.iso(p.end)})
	}
	return out
}
