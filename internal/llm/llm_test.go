package llm

import (
	"slices"
	"testing"
)

func TestResolveKnown(t *testing.T) {
	known := []string{"(toolkit) | PlayStation 3", "Pokémon Emerald | Game Boy Advance", "Super Mario 64 | Nintendo 64", "Banjo-Kazooie | N64"}
	titles, systems := resolveKnown(known,
		[]string{"Pokémon Emerald", "Pokemon Emerald", "Made Up Game", "Pokémon Emerald", "(toolkit)"},
		[]string{"Nintendo 64", "N64", "Atari 2600", "PlayStation 3"})
	if want := []string{"Pokémon Emerald"}; !slices.Equal(titles, want) {
		t.Errorf("titles = %v, want %v", titles, want)
	}
	if want := []string{"Nintendo 64", "N64", "PlayStation 3"}; !slices.Equal(systems, want) {
		t.Errorf("systems = %v, want %v", systems, want)
	}
}

func TestHasFilter(t *testing.T) {
	if (&Intent{Intent: "query"}).HasFilter() {
		t.Error("empty query should have no filter")
	}
	if !(&Intent{Systems: []string{"N64"}}).HasFilter() {
		t.Error("system should count as a filter")
	}
}
