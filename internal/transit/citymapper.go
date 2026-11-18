package transit

import (
	"net/url"
	"time"
)

const citymapperDirections = "https://citymapper.com/directions"

// citymapperURL: Citymapper takes "lat,lon" and an arrival time only; pass
// the zero time for a departure.
func (s *Service) citymapperURL(from, to *Resolved, arrival time.Time) string {
	q := url.Values{
		"startcoord": {formatFloat(from.Lat) + "," + formatFloat(from.Lon)},
		"startname":  {from.Name},
		"endcoord":   {formatFloat(to.Lat) + "," + formatFloat(to.Lon)},
		"endname":    {to.Name},
	}
	if !arrival.IsZero() {
		q.Set("arrival_time", s.iso(arrival))
	}
	return citymapperDirections + "?" + q.Encode()
}
