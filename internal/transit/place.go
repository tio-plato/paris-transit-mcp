package transit

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	ambiguityWindow = 8
	snapDistance    = 500
	communeLevel    = 8
)

var (
	latLon    = regexp.MustCompile(`^\s*(-?\d{1,3}(?:\.\d+)?)\s*,\s*(-?\d{1,3}(?:\.\d+)?)\s*$`)
	lonLatID  = regexp.MustCompile(`^(-?\d{1,3}(?:\.\d+)?);(-?\d{1,3}(?:\.\d+)?)$`)
	navitiaID = regexp.MustCompile(`^(stop_area|stop_point|address|poi|admin):[A-Za-z0-9:._-]+$`)
)

type place struct {
	Resolved

	// navitia is the value for from= and to=.
	navitia string
}

func (p *place) station() bool {
	return p.Type == typeStopArea || p.Type == typeStopPoint
}

func (p *place) endpoint() *Endpoint {
	return &Endpoint{Resolved: p.Resolved}
}

// coordPlace: the id is "lat,lon" so it round-trips. "lon;lat" is what
// Navitia uses for addresses, and those get looked up.
func coordPlace(lat, lon float64, name string) place {
	return place{
		Resolved: Resolved{
			ID: formatFloat(lat) + "," + formatFloat(lon), Name: name, Type: "coord", Lat: lat, Lon: lon,
			SnappedFrom: nil,
		},
		navitia: formatFloat(lon) + ";" + formatFloat(lat),
	}
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func (s *Service) resolvePlace(ctx context.Context, name, value string) (place, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return place{}, newError(CodeBadRequest, "%s is empty", name)
	}
	if m := latLon.FindStringSubmatch(v); m != nil {
		lat, lon := parseFloat(m[1]), parseFloat(m[2])
		if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
			return place{}, newError(CodeBadRequest, "%s %q is not a latitude,longitude pair", name, value)
		}
		return coordPlace(lat, lon, v), nil
	}
	if m := lonLatID.FindStringSubmatch(v); m != nil {
		// An address id, e.g. an ambiguous_place candidate.
		p, err := s.placeByID(ctx, name, v)
		if err != nil && AsToolError(err).Code == CodeNotFound {
			return coordPlace(parseFloat(m[2]), parseFloat(m[1]), v), nil
		}
		return p, err
	}
	if navitiaID.MatchString(v) {
		return s.placeByID(ctx, name, v)
	}
	return s.placeByText(ctx, name, v)
}

func (s *Service) placeByID(ctx context.Context, name, id string) (place, error) {
	var resp placesResponse
	err := s.prim.Navitia(ctx, "places/"+id, nil, &resp)
	if err != nil {
		if AsToolError(err).Code == CodeNotFound {
			return place{}, newError(CodeNotFound, "%s %q: no such place", name, id)
		}
		return place{}, fmt.Errorf("look up %s %q: %w", name, id, err)
	}
	if len(resp.Places) == 0 {
		return place{}, newError(CodeNotFound, "%s %q: no such place", name, id)
	}
	return fromNavitia(&resp.Places[0]), nil
}

func (s *Service) placeByText(ctx context.Context, name, text string) (place, error) {
	var resp placesResponse
	err := s.prim.Navitia(ctx, "places", url.Values{"q": {text}}, &resp)
	if err != nil {
		if AsToolError(err).Code == CodeNotFound {
			return place{}, newError(CodeNotFound, "%s %q: no place found", name, text)
		}
		return place{}, fmt.Errorf("search %s %q: %w", name, text, err)
	}
	if len(resp.Places) == 0 {
		return place{}, newError(CodeNotFound, "%s %q: no place found", name, text)
	}
	first := &resp.Places[0]
	if first.EmbeddedType == typeAddress {
		candidates := homonyms(resp.Places, text)
		if len(candidates) > 1 {
			return place{}, &ToolError{
				Code: CodeAmbiguousPlace,
				Message: name + " \"" + text + "\" matches the same address in several towns: ask the user which one, " +
					"then call again with that candidate's id",
				Candidates: candidates,
			}
		}
	}
	return fromNavitia(first), nil
}

// homonyms returns the same address in other towns among the first results,
// or nil if the query already names the first result's town.
func homonyms(places []navPlace, query string) []Candidate {
	if len(places) > ambiguityWindow {
		places = places[:ambiguityWindow]
	}
	first := &places[0]
	street := compact(stripTown(first.Name))
	firstTown := town(first)
	// Look for the town outside the street name: "12 rue de Paris" isn't Paris.
	rest := strings.Replace(compact(query), street, "", 1)
	if firstTown != "" && strings.Contains(rest, compact(firstTown)) {
		return nil
	}
	var out []Candidate
	towns := map[string]bool{}
	for i := range places {
		p := &places[i]
		if p.EmbeddedType != typeAddress || compact(stripTown(p.Name)) != street {
			continue
		}
		t := town(p)
		towns[compact(t)] = true
		r := fromNavitia(p)
		out = append(out, Candidate{
			ID: r.ID, Name: r.Name, Type: r.Type, Town: t, Lat: r.Lat, Lon: r.Lon,
			LineID: "", Line: "", Mode: "", Network: "",
		})
	}
	if len(towns) <= 1 {
		return nil
	}
	return out
}

func town(p *navPlace) string {
	if obj := p.object(); obj != nil {
		for _, r := range obj.AdministrativeRegions {
			if r.Level == communeLevel {
				return r.Name
			}
		}
	}
	return townInName(p.Name)
}

func fromNavitia(p *navPlace) place {
	r := Resolved{ID: p.ID, Name: p.Name, Type: p.EmbeddedType, Lat: 0, Lon: 0, SnappedFrom: nil}
	if obj := p.object(); obj != nil {
		r.Lat, r.Lon = parseFloat(obj.Coord.Lat), parseFloat(obj.Coord.Lon)
	}
	return place{Resolved: r, navitia: p.ID}
}

func (s *Service) snap(ctx context.Context, p place) (place, bool, error) {
	var resp placesNearbyResponse
	path := "coords/" + formatFloat(p.Lon) + ";" + formatFloat(p.Lat) + "/places_nearby"
	err := s.prim.Navitia(ctx, path, url.Values{
		"type[]":   {typeStopArea},
		"distance": {strconv.Itoa(snapDistance)},
	}, &resp)
	if err != nil {
		if AsToolError(err).Code == CodeNotFound {
			return place{}, false, nil
		}
		return place{}, false, fmt.Errorf("find a station near %q: %w", p.Name, err)
	}
	for i := range resp.PlacesNearby {
		near := &resp.PlacesNearby[i]
		if near.EmbeddedType != typeStopArea {
			continue
		}
		snapped := fromNavitia(near)
		distance, _ := strconv.Atoi(near.Distance)
		snapped.SnappedFrom = &SnappedFrom{Name: p.Name, DistanceM: distance}
		return snapped, true, nil
	}
	return place{}, false, nil
}

func (s *Service) stopArea(ctx context.Context, p place) (place, error) {
	if p.Type != typeStopPoint {
		return p, nil
	}
	var resp placesResponse
	err := s.prim.Navitia(ctx, "places/"+p.ID, nil, &resp)
	if err != nil {
		return place{}, fmt.Errorf("look up the stop area of %q: %w", p.ID, err)
	}
	if len(resp.Places) == 0 || resp.Places[0].StopPoint == nil || resp.Places[0].StopPoint.StopArea == nil {
		return place{}, newError(CodeNotFound, "stop point %q belongs to no stop area", p.ID)
	}
	sa := resp.Places[0].StopPoint.StopArea
	return place{
		Resolved: Resolved{
			ID: sa.ID, Name: sa.Name, Type: typeStopArea,
			Lat: parseFloat(sa.Coord.Lat), Lon: parseFloat(sa.Coord.Lon), SnappedFrom: nil,
		},
		navitia: sa.ID,
	}, nil
}
