package transit

// The parts of Navitia's responses the server reads.

// Navitia's embedded types of places.
const (
	typeStopArea  = "stop_area"
	typeStopPoint = "stop_point"
	typeAddress   = "address"
	typePOI       = "poi"
	typeAdmin     = "administrative_region"
)

// Query parameters sent with several requests.
const (
	paramCount          = "count"
	paramDisableGeoJSON = "disable_geojson"
	valueTrue           = "true"
)

type navCoord struct {
	Lat string `json:"lat"`
	Lon string `json:"lon"`
}

type navAdminRegion struct {
	Name  string `json:"name"`
	Level int    `json:"level"`
}

// navObject is any embedded Navitia place: stop area, stop point, address,
// point of interest or administrative region.
type navObject struct {
	ID                    string           `json:"id"`
	Name                  string           `json:"name"`
	Label                 string           `json:"label"`
	Coord                 navCoord         `json:"coord"`
	AdministrativeRegions []navAdminRegion `json:"administrative_regions"`
	StopArea              *navObject       `json:"stop_area"`
}

type navPlace struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	EmbeddedType         string     `json:"embedded_type"`
	Distance             string     `json:"distance"`
	StopArea             *navObject `json:"stop_area"`
	StopPoint            *navObject `json:"stop_point"`
	Address              *navObject `json:"address"`
	POI                  *navObject `json:"poi"`
	AdministrativeRegion *navObject `json:"administrative_region"`
}

// object is the embedded object the place's type names.
func (p *navPlace) object() *navObject {
	switch p.EmbeddedType {
	case typeStopArea:
		return p.StopArea
	case typeStopPoint:
		return p.StopPoint
	case typeAddress:
		return p.Address
	case typePOI:
		return p.POI
	case typeAdmin:
		return p.AdministrativeRegion
	default:
		return nil
	}
}

type placesResponse struct {
	Places []navPlace `json:"places"`
}

type placesNearbyResponse struct {
	PlacesNearby []navPlace `json:"places_nearby"`
}

type navLink struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Rel  string `json:"rel"`
}

type navDisplayInfo struct {
	Direction      string    `json:"direction"`
	Code           string    `json:"code"`
	CommercialMode string    `json:"commercial_mode"`
	TripShortName  string    `json:"trip_short_name"`
	Links          []navLink `json:"links"`
}

type navSection struct {
	Type                  string          `json:"type"`
	From                  *navPlace       `json:"from"`
	To                    *navPlace       `json:"to"`
	DepartureDateTime     string          `json:"departure_date_time"`
	ArrivalDateTime       string          `json:"arrival_date_time"`
	BaseDepartureDateTime string          `json:"base_departure_date_time"`
	DisplayInformations   *navDisplayInfo `json:"display_informations"`
	Links                 []navLink       `json:"links"`
	StopDateTimes         []struct{}      `json:"stop_date_times"`
}

func (s *navSection) linkID(typ string) string {
	for _, l := range s.Links {
		if l.Type == typ {
			return l.ID
		}
	}
	return ""
}

type navJourney struct {
	Type              string `json:"type"`
	Duration          int    `json:"duration"`
	NbTransfers       int    `json:"nb_transfers"`
	DepartureDateTime string `json:"departure_date_time"`
	ArrivalDateTime   string `json:"arrival_date_time"`
	Durations         struct {
		Walking int `json:"walking"`
	} `json:"durations"`
	Sections []navSection `json:"sections"`
}

// publicTransport returns the journey's public transport sections.
func (j *navJourney) publicTransport() []*navSection {
	var out []*navSection
	for i := range j.Sections {
		if j.Sections[i].Type == "public_transport" {
			out = append(out, &j.Sections[i])
		}
	}
	return out
}

type navPeriod struct {
	Begin string `json:"begin"`
	End   string `json:"end"`
}

type navMessage struct {
	Text    string `json:"text"`
	Channel struct {
		ContentType string   `json:"content_type"`
		Types       []string `json:"types"`
	} `json:"channel"`
}

type navDisruption struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	Tags     []string `json:"tags"`
	Severity struct {
		Name   string `json:"name"`
		Effect string `json:"effect"`
	} `json:"severity"`
	ApplicationPeriods []navPeriod  `json:"application_periods"`
	Messages           []navMessage `json:"messages"`
	ImpactedObjects    []struct {
		PtObject navPlace `json:"pt_object"`
	} `json:"impacted_objects"`
}

type journeysResponse struct {
	Journeys    []navJourney    `json:"journeys"`
	Disruptions []navDisruption `json:"disruptions"`
}

type navLine struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Code           string `json:"code"`
	CommercialMode struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"commercial_mode"`
	Network struct {
		Name string `json:"name"`
	} `json:"network"`
}

type linesResponse struct {
	Lines []navLine `json:"lines"`
}

type ptObjectsResponse struct {
	PtObjects []struct {
		EmbeddedType string  `json:"embedded_type"`
		Line         navLine `json:"line"`
	} `json:"pt_objects"`
}

type lineReportsResponse struct {
	LineReports []struct {
		Line navLine `json:"line"`
	} `json:"line_reports"`
	Disruptions []navDisruption `json:"disruptions"`
}

type departuresResponse struct {
	Departures []struct {
		DisplayInformations navDisplayInfo `json:"display_informations"`
		StopDateTime        struct {
			DepartureDateTime string `json:"departure_date_time"`
		} `json:"stop_date_time"`
		Route struct {
			ID            string `json:"id"`
			DirectionType string `json:"direction_type"`
			Direction     struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"direction"`
		} `json:"route"`
	} `json:"departures"`
}
