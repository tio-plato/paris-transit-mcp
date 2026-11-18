package transit

import (
	"regexp"
	"strings"
	"unicode"
)

var trailingParens = regexp.MustCompile(`\s*\([^()]*\)\s*$`)

// stripTown removes Navitia's trailing "(Town)".
func stripTown(name string) string {
	return strings.TrimSpace(trailingParens.ReplaceAllString(name, ""))
}

func townInName(name string) string {
	m := trailingParens.FindString(name)
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(m), "()"))
}

// fold lowercases and strips accents.
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		b.WriteString(unaccent(r))
	}
	return b.String()
}

func unaccent(r rune) string {
	switch r {
	case 'à', 'â', 'ä', 'á', 'ã', 'å':
		return "a"
	case 'ç':
		return "c"
	case 'é', 'è', 'ê', 'ë':
		return "e"
	case 'î', 'ï', 'í', 'ì':
		return "i"
	case 'ô', 'ö', 'ó', 'ò', 'õ':
		return "o"
	case 'ù', 'û', 'ü', 'ú':
		return "u"
	case 'ÿ':
		return "y"
	case 'ñ':
		return "n"
	case 'œ':
		return "oe"
	case 'æ':
		return "ae"
	default:
		return string(r)
	}
}

// compact keeps letters and digits: "Massy - Palaiseau" == "Massy-Palaiseau".
func compact(s string) string {
	var b strings.Builder
	for _, r := range fold(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func sameStop(a, b string) bool {
	ca, cb := compact(stripTown(a)), compact(stripTown(b))
	return ca != "" && ca == cb
}
