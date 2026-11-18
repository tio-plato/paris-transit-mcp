package transit

import (
	"regexp"
	"strings"
	"unicode"
)

var trailingParens = regexp.MustCompile(`\s*\([^()]*\)\s*$`)

// stripTown removes a trailing "(…)", where Navitia writes the town.
func stripTown(name string) string {
	return strings.TrimSpace(trailingParens.ReplaceAllString(name, ""))
}

// townInName is the trailing "(…)" of a Navitia name, without the brackets.
func townInName(name string) string {
	m := trailingParens.FindString(name)
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(m), "()"))
}

// fold lowercases s and removes the accents of French and nearby languages.
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

// compact folds s and keeps only letters and digits, so that
// "Massy - Palaiseau" and "Massy-Palaiseau" compare equal.
func compact(s string) string {
	var b strings.Builder
	for _, r := range fold(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sameStop reports whether two stop names name the same stop once the town,
// accents, spaces and hyphens are removed.
func sameStop(a, b string) bool {
	ca, cb := compact(stripTown(a)), compact(stripTown(b))
	return ca != "" && ca == cb
}
