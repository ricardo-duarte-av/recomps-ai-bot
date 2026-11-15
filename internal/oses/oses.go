// Package oses defines the canonical list of target operating systems/platforms
// and normalizes free-form names (distros, devices, nicknames) onto it.
package oses

import (
	"slices"
	"strings"
)

// Canonical OS identifiers stored in the database.
const (
	Windows = "windows"
	Linux   = "linux"
	MacOS   = "macos"
	Android = "android"
	IOS     = "ios"
	Switch  = "switch"
	Web     = "web"
	FreeBSD = "freebsd"
)

// All is the ordered list of canonical OS identifiers.
var All = []string{Windows, Linux, MacOS, Android, IOS, Switch, Web, FreeBSD}

var displayNames = map[string]string{
	Windows: "Windows",
	Linux:   "Linux",
	MacOS:   "macOS",
	Android: "Android",
	IOS:     "iOS",
	Switch:  "Switch",
	Web:     "Web",
	FreeBSD: "FreeBSD",
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
	"flatpak": Linux, "appimage": Linux,
	// macOS
	"macos": MacOS, "mac os": MacOS, "mac": MacOS, "osx": MacOS, "os x": MacOS, "mac os x": MacOS,
	"darwin": MacOS, "apple silicon": MacOS,
	// Mobile
	"android": Android, "ios": IOS, "iphone": IOS, "ipad": IOS, "ipados": IOS,
	// Consoles
	"switch": Switch, "nintendo switch": Switch, "nx": Switch,
	// Web
	"web": Web, "browser": Web, "wasm": Web, "webassembly": Web, "emscripten": Web, "html5": Web,
	// BSD
	"freebsd": FreeBSD,
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
