package oses

import (
	"slices"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Steam Deck": Linux, "steamdeck": Linux, "Fedora": Linux, "ubuntu": Linux,
		"Debian": Linux, "Arch": Linux, "OSX": MacOS, "macOS": MacOS, "Mac OS X": MacOS,
		"Win64": Windows, "Windows  11": Windows, "Nintendo Switch": Switch, "WASM": Web,
		"Nintendo 3DS": ThreeDS, "New 3DS": ThreeDS, "2DS": ThreeDS, "PS Vita": Vita, "Wii U": WiiU,
		"Xbox Series X|S": XboxOne, "Dreamcast": Dreamcast, "NDS": DS,
	}
	for in, want := range cases {
		got, ok := Normalize(in)
		if !ok || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := Normalize("amiga"); ok {
		t.Error("amiga should be unknown")
	}
}

func TestNormalizeList(t *testing.T) {
	known, unknown := NormalizeList([]string{"macos", "Linux", "steamdeck", "osx", "Windows", "amiga"})
	if want := []string{Windows, Linux, MacOS}; !slices.Equal(known, want) {
		t.Errorf("known = %v, want %v", known, want)
	}
	if !slices.Equal(unknown, []string{"amiga"}) {
		t.Errorf("unknown = %v", unknown)
	}
}

func TestJoinDisplay(t *testing.T) {
	if got := JoinDisplay([]string{Windows, Linux, MacOS}); got != "Windows, Linux and macOS" {
		t.Errorf("got %q", got)
	}
	if got := JoinDisplay([]string{Linux}); got != "Linux" {
		t.Errorf("got %q", got)
	}
}

func TestConsoleTargetsHaveDisplayNames(t *testing.T) {
	for _, os := range All {
		if Display(os) == os {
			t.Errorf("%q has no display name", os)
		}
		if got, ok := Normalize(os); !ok || got != os {
			t.Errorf("canonical %q does not normalize to itself", os)
		}
	}
	if got := JoinDisplay([]string{ThreeDS}); got != "Nintendo 3DS" {
		t.Errorf("got %q", got)
	}
}
