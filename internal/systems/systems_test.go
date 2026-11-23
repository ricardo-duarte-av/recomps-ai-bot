package systems

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"PS5":                                "PlayStation 5",
		"Switch2":                            "Nintendo Switch 2",
		"Nintendo Switch (Switch 2 Edition)": "Nintendo Switch 2",
		"Nintendo Switch":                    "Nintendo Switch",
		"nintendo  64":                       "Nintendo 64",
		"Windows (PC)":                       "Windows",
		"DOS (MS‑DOS)":                       "MS-DOS",
		"CP System II (arcade)":              "Arcade (CPS-2)",
		"Xbox Series X|S":                    "Xbox Series X/S",
		"Original Xbox":                      "Xbox",
		// Unknown names are kept, only tidied.
		"Taito  F3 System": "Taito F3 System",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAliasesPointToCanonical(t *testing.T) {
	for alias, c := range aliases {
		if Normalize(c) != c {
			t.Errorf("alias %q maps to %q, which doesn't normalize to itself", alias, c)
		}
		if alias != key(alias) {
			t.Errorf("alias %q isn't a normalized key", alias)
		}
	}
	seen := map[string]bool{}
	for _, c := range Canonical {
		if seen[key(c)] || strings.TrimSpace(c) != c {
			t.Errorf("bad or duplicate canonical name %q", c)
		}
		seen[key(c)] = true
	}
}
