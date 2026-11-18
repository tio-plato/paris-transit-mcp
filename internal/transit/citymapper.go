package transit

import (
	"net/url"
	"time"
)

const citymapperDirections = "https://citymapper.com/directions"

// citymapperURL links to the same trip in Citymapper. Citymapper takes
// coordinates as "lat,lon" and only an arrival time, so arrival is the zero
// time for a trip asked by departure.
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
