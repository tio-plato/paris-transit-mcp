package transit

import (
	"context"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// maxLineMatches is how many lines a line search asks for: a number such as
// 14 also matches 140 to 149 and more, which come before rarer exact matches.
const maxLineMatches = 100

// Modes the server names lines by.
const (
	modeMetro      = "Metro"
	modeRER        = "RER"
	modeTransilien = "Transilien"
	modeTER        = "TER"
	modeTram       = "Tram"
	modeBus        = "Bus"
	modeNoctilien  = "Noctilien"
)

var noctilienCode = regexp.MustCompile(`^N\d+$`)

// modeOf names the mode of a line from its Navitia commercial mode and code.
func modeOf(commercialMode, commercialModeName, code string) string {
	switch commercialMode {
	case "commercial_mode:Metro":
		return modeMetro
	case "commercial_mode:RapidTransit":
		return modeRER
	case "commercial_mode:LocalTrain":
		return modeTransilien
	case "commercial_mode:regionalRail":
		return modeTER
	case "commercial_mode:Tramway":
		return modeTram
	case "commercial_mode:Bus":
		if noctilienCode.MatchString(code) {
			return modeNoctilien
		}
		return modeBus
	}
	// Journey sections give the commercial mode by name only.
	switch fold(commercialModeName) {
	case "metro":
		return modeMetro
	case "rer":
		return modeRER
	case "train transilien", "transilien":
		return modeTransilien
	case "ter":
		return modeTER
	case "tramway":
		return modeTram
	case "bus":
		if noctilienCode.MatchString(code) {
			return modeNoctilien
		}
		return modeBus
	}
	return commercialModeName
}

// rail reports whether a mode runs on rails: metro, RER, train or tram.
func rail(mode string) bool {
	switch mode {
	case modeMetro, modeRER, modeTransilien, modeTER, modeTram:
		return true
	default:
		return false
	}
}

// modeRank orders modes for next_departures: RER and trains, metro, tram,
// others, bus, Noctilien.
func modeRank(mode string) int {
	order := [][]string{
		{modeRER, modeTransilien, modeTER},
		{modeMetro},
		{modeTram},
		nil, // any other mode
		{modeBus},
		{modeNoctilien},
	}
	other := 0
	for rank, modes := range order {
		if modes == nil {
			other = rank
		}
		if slices.Contains(modes, mode) {
			return rank
		}
	}
	return other
}

// label is the short name of a line, such as M14, RER B, T3a or Bus 21.
func label(mode, code string) string {
	switch mode {
	case modeMetro:
		return "M" + code
	case modeRER, modeTransilien, modeBus:
		return mode + " " + code
	default:
		return code
	}
}

// line is a resolved line.
type line struct {
	ID      string
	Code    string
	Mode    string
	Network string
}

func lineFromNavitia(l *navLine) line {
	return line{
		ID:      l.ID,
		Code:    l.Code,
		Mode:    modeOf(l.CommercialMode.ID, l.CommercialMode.Name, l.Code),
		Network: l.Network.Name,
	}
}

func (l *line) label() string { return label(l.Mode, l.Code) }

func (l *line) candidate() Candidate {
	return Candidate{
		ID: "", Name: "", Type: "", Town: "", Lat: 0, Lon: 0,
		LineID: l.ID, Line: l.label(), Mode: l.Mode, Network: l.Network,
	}
}

// lineQuery is a parsed line name: the mode it names, if any, and the code.
type lineQuery struct {
	modes []string
	code  string
}

var (
	lineIDPattern = regexp.MustCompile(`^line:[A-Za-z0-9:_-]+$`)
	metroShort    = regexp.MustCompile(`^m\s*(\d+(?:bis)?)$`)
	tramShort     = regexp.MustCompile(`^t\d+[a-z]?$`)
)

// modeWord is a word that names a mode in a line name.
type modeWord struct {
	word  string
	modes []string
	// prefix is prepended to the code when it lacks it: "Tram 3a" is T3a.
	prefix string
}

func modeWords() []modeWord {
	return []modeWord{
		{"metro", []string{modeMetro}, ""},
		{"métro", []string{modeMetro}, ""},
		{"rer", []string{modeRER}, ""},
		{"tram", []string{modeTram}, "t"},
		{"tramway", []string{modeTram}, "t"},
		{"bus", []string{modeBus, modeNoctilien}, ""},
		{"transilien", []string{modeTransilien, modeTER}, ""},
		{"train", []string{modeTransilien, modeTER}, ""},
		{"noctilien", []string{modeNoctilien}, "n"},
	}
}

// parseLineName reads a line name such as "M14", "Métro 14", "RER B", "T3a",
// "Tram 3a", "Bus 21", "Transilien J", "Noctilien N14", or a bare code.
func parseLineName(name string) lineQuery {
	v := strings.Join(strings.Fields(strings.ToLower(name)), " ")
	if m := metroShort.FindStringSubmatch(v); m != nil {
		return lineQuery{modes: []string{modeMetro}, code: m[1]}
	}
	if tramShort.MatchString(v) {
		return lineQuery{modes: []string{modeTram}, code: v}
	}
	for _, mw := range modeWords() {
		rest, ok := strings.CutPrefix(v, mw.word+" ")
		if !ok {
			continue
		}
		code := strings.TrimSpace(rest)
		if !strings.HasPrefix(code, mw.prefix) {
			code = mw.prefix + code
		}
		return lineQuery{modes: mw.modes, code: code}
	}
	return lineQuery{modes: nil, code: v}
}

// match picks the line a query names among lines: exact code, within the
// query's mode if it has one. Without a mode, one rail line among several
// matches wins. It returns the matches when there is no single answer.
func (q lineQuery) match(lines []line) (line, []line) {
	var matches []line
	seen := map[string]bool{}
	for _, l := range lines {
		if seen[l.ID] || !strings.EqualFold(l.Code, q.code) {
			continue
		}
		if q.modes != nil && !slices.Contains(q.modes, l.Mode) {
			continue
		}
		seen[l.ID] = true
		matches = append(matches, l)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if q.modes == nil && len(matches) > 1 {
		var rails []line
		for _, l := range matches {
			if rail(l.Mode) {
				rails = append(rails, l)
			}
		}
		if len(rails) == 1 {
			return rails[0], nil
		}
	}
	return line{}, matches
}

// lineError is the error for a line name that names no single line.
func lineError(name string, matches []line) *ToolError {
	if len(matches) == 0 {
		return newError(CodeNotFound, "line %q: no such line", name)
	}
	candidates := make([]Candidate, 0, len(matches))
	for i := range matches {
		candidates = append(candidates, matches[i].candidate())
	}
	return &ToolError{
		Code: CodeAmbiguousLine,
		Message: "line \"" + name + "\" matches several lines: ask the user which one, " +
			"then call again with that candidate's line_id",
		Candidates: candidates,
	}
}

// resolveLine resolves a line name or id across the whole network.
func (s *Service) resolveLine(ctx context.Context, name string) (line, error) {
	v := strings.TrimSpace(name)
	if lineIDPattern.MatchString(v) {
		var resp linesResponse
		err := s.prim.Navitia(ctx, "lines/"+v, url.Values{"depth": {"1"}}, &resp)
		if err != nil {
			if AsToolError(err).Code == CodeNotFound {
				return line{}, newError(CodeNotFound, "line %q: no such line", name)
			}
			return line{}, err
		}
		if len(resp.Lines) == 0 {
			return line{}, newError(CodeNotFound, "line %q: no such line", name)
		}
		return lineFromNavitia(&resp.Lines[0]), nil
	}
	q := parseLineName(v)
	if q.code == "" {
		return line{}, newError(CodeBadRequest, "line name %q has no line number", name)
	}
	var resp ptObjectsResponse
	err := s.prim.Navitia(ctx, "pt_objects", url.Values{
		"q": {q.code}, "type[]": {"line"}, paramCount: {strconv.Itoa(maxLineMatches)},
	}, &resp)
	if err != nil {
		if AsToolError(err).Code == CodeNotFound {
			return line{}, lineError(name, nil)
		}
		return line{}, err
	}
	lines := make([]line, 0, len(resp.PtObjects))
	for i := range resp.PtObjects {
		if resp.PtObjects[i].EmbeddedType == "line" {
			lines = append(lines, lineFromNavitia(&resp.PtObjects[i].Line))
		}
	}
	l, matches := q.match(lines)
	if l.ID == "" {
		return line{}, lineError(name, matches)
	}
	return l, nil
}

var leadingNumber = regexp.MustCompile(`^[A-Za-z]?(\d+)`)

// codeKey sorts line codes naturally: 2 before 10, T2 before T10, numbers
// before letters.
func codeKey(code string) (int, string) {
	m := leadingNumber.FindStringSubmatch(code)
	if m == nil {
		return math.MaxInt, code
	}
	n, _ := strconv.Atoi(m[1])
	return n, code
}
