package transit

import (
	"strings"
	"time"
)

const (
	navitiaLayout = "20060102T150405"
	// maxPast is how far in the past a time parameter may be: an earlier time
	// is most likely a date the client got wrong.
	maxPast = time.Hour
)

// parseTime reads an ISO 8601 time parameter; name is the parameter, for the error.
func (s *Service) parseTime(name, value string) (time.Time, error) {
	v := strings.TrimSpace(value)
	// Layouts with an offset, then without, which are Paris time.
	offsetLayouts := []string{time.RFC3339, "2006-01-02T15:04Z07:00"}
	localLayouts := []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"}
	var t time.Time
	var err error
	for _, layout := range offsetLayouts {
		t, err = time.Parse(layout, v)
		if err == nil {
			break
		}
	}
	if err != nil {
		for _, layout := range localLayouts {
			t, err = time.ParseInLocation(layout, v, s.paris)
			if err == nil {
				break
			}
		}
	}
	if err != nil {
		return time.Time{}, newError(CodeBadRequest,
			"%s %q is not an ISO 8601 time such as 2026-11-20T08:30 (Paris time) or 2026-11-20T08:30:00+01:00",
			name, value)
	}
	now := s.now()
	if t.Before(now.Add(-maxPast)) {
		return time.Time{}, newError(CodeBadRequest,
			"%s %q is more than an hour before now (%s): check the date",
			name, value, now.In(s.paris).Format(time.RFC3339))
	}
	return t.In(s.paris), nil
}

// navitiaTime formats t for a Navitia query, in Paris time.
func (s *Service) navitiaTime(t time.Time) string {
	return t.In(s.paris).Format(navitiaLayout)
}

// parseNavitia reads a Navitia time, which is Paris time without an offset.
func (s *Service) parseNavitia(v string) (time.Time, bool) {
	t, err := time.ParseInLocation(navitiaLayout, v, s.paris)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// iso formats t as ISO 8601 Paris time with its offset.
func (s *Service) iso(t time.Time) string {
	return t.In(s.paris).Format(time.RFC3339)
}

// isoNavitia converts a Navitia time to ISO 8601 Paris time; an unreadable
// time is returned as it is.
func (s *Service) isoNavitia(v string) string {
	t, ok := s.parseNavitia(v)
	if !ok {
		return v
	}
	return s.iso(t)
}

// minutesUntil is the whole number of minutes from now to t, never negative.
func (s *Service) minutesUntil(t time.Time) int {
	d := t.Sub(s.now())
	if d < 0 {
		return 0
	}
	return int(d / time.Minute)
}

// roundMinutes rounds a number of seconds to the nearest minute.
func roundMinutes(seconds int) int {
	return int((time.Duration(seconds) * time.Second).Round(time.Minute) / time.Minute)
}
