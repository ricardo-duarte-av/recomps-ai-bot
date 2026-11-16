// Package oses defines the canonical list of target platforms (operating systems,
// consoles and handhelds)
// and normalizes free-form names (distros, devices, nicknames) onto it.
package oses

import (
	"slices"
	"strings"
)

// Canonical target platform identifiers stored in the database. Ports can target
// desktop/mobile operating systems or other consoles/handhelds.
const (
	Windows = "windows"
	Linux   = "linux"
	MacOS   = "macos"
	FreeBSD = "freebsd"
	Haiku   = "haiku"
	DOS     = "dos"
	Android = "android"
	IOS     = "ios"
	Web     = "web"

	Switch    = "switch"
	ThreeDS   = "3ds"
	DS        = "nds"
	WiiU      = "wiiu"
	Wii       = "wii"
	GameCube  = "gamecube"
	N64       = "n64"
	GBA       = "gba"
	Vita      = "vita"
	PSP       = "psp"
	PS2       = "ps2"
	PS3       = "ps3"
	PS4       = "ps4"
	PS5       = "ps5"
	Xbox      = "xbox"
	Xbox360   = "xbox360"
	XboxOne   = "xboxone"
	Dreamcast = "dreamcast"
)

// All is the ordered list of canonical platform identifiers.
var All = []string{
	Windows, Linux, MacOS, FreeBSD, Haiku, DOS, Android, IOS, Web,
	Switch, ThreeDS, DS, WiiU, Wii, GameCube, N64, GBA,
	Vita, PSP, PS2, PS3, PS4, PS5, Xbox, Xbox360, XboxOne, Dreamcast,
}

var displayNames = map[string]string{
	Windows: "Windows", Linux: "Linux", MacOS: "macOS", FreeBSD: "FreeBSD", Haiku: "Haiku", DOS: "DOS",
	Android: "Android", IOS: "iOS", Web: "Web",
	Switch: "Nintendo Switch", ThreeDS: "Nintendo 3DS", DS: "Nintendo DS", WiiU: "Wii U", Wii: "Wii",
	GameCube: "GameCube", N64: "Nintendo 64", GBA: "Game Boy Advance",
	Vita: "PS Vita", PSP: "PSP", PS2: "PS2", PS3: "PS3", PS4: "PS4", PS5: "PS5",
	Xbox: "Xbox", Xbox360: "Xbox 360", XboxOne: "Xbox One/Series", Dreamcast: "Dreamcast",
}

var aliases = map[string]string{
	// Windows
	"windows": Windows, "win": Windows, "win32": Windows, "win64": Windows, "pc": Windows,
	"windows 10": Windows, "windows 11": Windows, "windows 7": Windows, "microsoft windows": Windows,
	// Linux and things that run Linux builds
	"linux": Linux, "gnu/linux": Linux, "steamdeck": Linux, "steam deck": Linux, "steamos": Linux,
	"fedora": Linux, "ubuntu": Linux, "debian": Linux, "arch": Linux, "arch linux": Linux,
	"archlinux": Linux, "mint": Linux, "linux mint": Linux, "manjaro": Linux, "opensuse": Linux,
	"nixos": Linux, "pop!_os": Linux, "popos": Linux, "gentoo": Linux, "bazzite": Linux,
	"flatpak": Linux, "appimage": Linux, "raspberry pi": Linux, "raspberrypi": Linux,
	// macOS
	"macos": MacOS, "mac os": MacOS, "mac": MacOS, "osx": MacOS, "os x": MacOS, "mac os x": MacOS,
	"darwin": MacOS, "apple silicon": MacOS,
	// Other desktop
	"freebsd": FreeBSD, "haiku": Haiku, "dos": DOS, "ms-dos": DOS, "msdos": DOS,
	// Mobile
	"android": Android, "ios": IOS, "iphone": IOS, "ipad": IOS, "ipados": IOS,
	// Web
	"web": Web, "browser": Web, "wasm": Web, "webassembly": Web, "emscripten": Web, "html5": Web,
	// Nintendo
	"switch": Switch, "nintendo switch": Switch, "nx": Switch, "switch 2": Switch, "nintendo switch 2": Switch,
	"3ds": ThreeDS, "nintendo 3ds": ThreeDS, "n3ds": ThreeDS, "new 3ds": ThreeDS, "new nintendo 3ds": ThreeDS,
	"2ds": ThreeDS, "3ds/2ds": ThreeDS,
	"nds": DS, "ds": DS, "nintendo ds": DS, "dsi": DS, "nintendo dsi": DS,
	"wiiu": WiiU, "wii u": WiiU, "nintendo wii u": WiiU,
	"wii": Wii, "nintendo wii": Wii,
	"gamecube": GameCube, "game cube": GameCube, "gc": GameCube, "ngc": GameCube, "nintendo gamecube": GameCube,
	"n64": N64, "nintendo 64": N64,
	"gba": GBA, "game boy advance": GBA, "gameboy advance": GBA,
	// Sony
	"vita": Vita, "ps vita": Vita, "psvita": Vita, "playstation vita": Vita,
	"psp": PSP, "playstation portable": PSP,
	"ps2": PS2, "playstation 2": PS2, "ps3": PS3, "playstation 3": PS3,
	"ps4": PS4, "playstation 4": PS4, "ps5": PS5, "playstation 5": PS5,
	// Microsoft
	"xbox": Xbox, "original xbox": Xbox, "xbox 360": Xbox360, "xbox360": Xbox360, "x360": Xbox360,
	"xbox one": XboxOne, "xboxone": XboxOne, "xbox series": XboxOne, "xbox series x": XboxOne,
	"xbox series s": XboxOne, "xbox series x|s": XboxOne, "xbox series x/s": XboxOne,
	// Sega
	"dreamcast": Dreamcast, "sega dreamcast": Dreamcast, "dc": Dreamcast,
}

// Normalize maps a single free-form OS name onto its canonical identifier.
// It returns false if the name is not recognized.
func Normalize(name string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	key = strings.Join(strings.Fields(key), " ")
	if os, ok := aliases[key]; ok {
		return os, true
	}
	return "", false
}

// NormalizeList normalizes and deduplicates a list of OS names, returning the
// canonical identifiers in canonical order plus any names that were not recognized.
func NormalizeList(names []string) (known []string, unknown []string) {
	seen := map[string]bool{}
	for _, n := range names {
		if os, ok := Normalize(n); ok {
			seen[os] = true
		} else if strings.TrimSpace(n) != "" {
			unknown = append(unknown, n)
		}
	}
	for _, os := range All {
		if seen[os] {
			known = append(known, os)
		}
	}
	return known, unknown
}

// IsCanonical reports whether os is a canonical identifier.
func IsCanonical(os string) bool {
	return slices.Contains(All, os)
}

// Display returns the human-readable name of a canonical OS.
func Display(os string) string {
	if d, ok := displayNames[os]; ok {
		return d
	}
	return os
}

// JoinDisplay renders a list of canonical OSes as "Windows, Linux and macOS".
func JoinDisplay(list []string) string {
	names := make([]string, len(list))
	for i, os := range list {
		names[i] = Display(os)
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}
