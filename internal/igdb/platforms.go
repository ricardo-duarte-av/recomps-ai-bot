package igdb

import (
	"strings"
	"unicode"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/systems"
)

// platformNames maps canonical systems (see package systems) whose IGDB name
// differs from ours onto IGDB platform names, best match first. Systems not
// listed are matched by their own name against IGDB's names, alternative names
// and abbreviations.
var platformNames = map[string][]string{
	"GameCube":                  {"Nintendo GameCube"},
	"Famicom Disk System":       {"Family Computer Disk System"},
	"Sega Master System":        {"Sega Master System/Mark III"},
	"Sega Genesis / Mega Drive": {"Sega Mega Drive/Genesis"},
	"Mega-CD (Sega CD)":         {"Sega CD"},
	"Game Gear":                 {"Sega Game Gear"},
	"Sega NAOMI":                {"Sega NAOMI", "Arcade"},
	"Neo Geo":                   {"Neo Geo AES", "Neo Geo MVS"},
	"PC Engine (TurboGrafx-16)": {"TurboGrafx-16/PC Engine"},
	"3DO":                       {"3DO Interactive Multiplayer"},
	"Hyper Neo Geo 64":          {"Hyper Neo Geo 64", "Arcade"},
	"MS-DOS":                    {"DOS"},
	"MS-DOS / Windows":          {"DOS", "PC (Microsoft Windows)"},
	"Windows":                   {"PC (Microsoft Windows)"},
	"macOS":                     {"Mac"},
	"Commodore 64":              {"Commodore C64/128/MAX"},
	"NEC PC-98":                 {"PC-9800 Series"},
	"J2ME (Java ME)":            {"Legacy Mobile Device"},
}

// key reduces a title or platform name to lowercase letters and digits, so
// "Xbox Series X|S" and "Xbox Series X/S" or "Pokémon" spacing variants compare equal.
func key(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchSystem finds the IGDB platforms for an original system. single reports
// whether system names one system (possibly with fallbacks, the first being
// the best), rather than a list like "Windows, PlayStation 3, Xbox 360".
func matchSystem(all []Platform, system string) (plats []Platform, single bool) {
	if p := matchOne(all, system); len(p) > 0 {
		return p, true
	}
	// Split lists like "Windows, PlayStation 3" or "PlayStation 5 / Xbox Series X/S",
	// but not names with a bare slash such as "Xbox Series X/S".
	parts := strings.FieldsFunc(strings.NewReplacer(" / ", ",", " & ", ",").Replace(system),
		func(r rune) bool { return r == ',' })
	if len(parts) < 2 {
		return nil, false
	}
	seen := map[int]bool{}
	for _, part := range parts {
		for _, p := range matchOne(all, part) {
			if !seen[p.ID] {
				seen[p.ID] = true
				plats = append(plats, p)
			}
		}
	}
	return plats, len(plats) == 1
}

// matchOne matches one system name, trying our known IGDB names, then the name
// itself, then the parts outside and inside parentheses ("Arcade (CPS-2)").
func matchOne(all []Platform, name string) []Platform {
	name = systems.Normalize(name)
	if name == "" {
		return nil
	}
	candidates := append([]string{}, platformNames[name]...)
	candidates = append(candidates, name)
	if before, inside, ok := strings.Cut(name, "("); ok {
		candidates = append(candidates, before, strings.TrimSuffix(strings.TrimSpace(inside), ")"))
	}
	var out []Platform
	seen := map[int]bool{}
	for _, c := range candidates {
		k := key(c)
		if k == "" {
			continue
		}
		for _, p := range all {
			if !seen[p.ID] && platformHasKey(p, k) {
				seen[p.ID] = true
				out = append(out, p)
			}
		}
		if len(out) > 0 && len(platformNames[name]) == 0 {
			break // our own name matched; don't go on to looser candidates
		}
	}
	return out
}

func platformHasKey(p Platform, k string) bool {
	if key(p.Name) == k || key(p.Abbreviation) == k {
		return true
	}
	for _, alt := range strings.Split(p.AlternativeName, ",") {
		if key(alt) == k {
			return true
		}
	}
	return false
}
