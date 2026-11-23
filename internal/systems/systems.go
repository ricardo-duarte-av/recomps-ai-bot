// Package systems defines canonical names for the original systems games came
// from (consoles, handhelds, arcade boards, computers) and maps the many ways a
// README or a user spells them ("PS5", "Switch2", "Windows (PC)") onto one name,
// so the list doesn't split one system across several spellings.
package systems

import (
	"strings"
)

// Canonical lists the preferred spelling of common original systems. The model
// is told to use one of these when it fits; names outside it (rare arcade boards,
// multi-system releases) are kept as written.
var Canonical = []string{
	// Nintendo
	"Nintendo Entertainment System", "Famicom Disk System", "Super Nintendo Entertainment System",
	"Nintendo 64", "GameCube", "Wii", "Wii U", "Nintendo Switch", "Nintendo Switch 2",
	"Game Boy", "Game Boy Color", "Game Boy Advance", "Nintendo DS", "Nintendo 3DS", "Virtual Boy",
	// Sony
	"PlayStation", "PlayStation 2", "PlayStation 3", "PlayStation 4", "PlayStation 5",
	"PlayStation Portable", "PlayStation Vita",
	// Microsoft
	"Xbox", "Xbox 360", "Xbox One", "Xbox Series X/S",
	// Sega
	"Sega Master System", "Sega Genesis / Mega Drive", "Mega-CD (Sega CD)", "Sega 32X", "Sega Saturn",
	"Dreamcast", "Sega NAOMI", "Game Gear",
	// Other consoles and handhelds
	"Neo Geo", "Neo Geo Pocket Color", "PC Engine (TurboGrafx-16)", "Atari 2600", "Atari Jaguar",
	"Philips CD-i", "3DO",
	// Arcade
	"Arcade", "Arcade (CPS-1)", "Arcade (CPS-2)", "Arcade (CPS-3)", "Arcade (Sega Model 2)",
	"Arcade (Sega Model 3)", "Arcade (Sega Lindbergh)", "Hyper Neo Geo 64",
	// Computers and mobile
	"MS-DOS", "MS-DOS / Windows", "Windows", "macOS", "Linux", "Amiga", "ZX Spectrum", "Commodore 64",
	"NEC PC-98", "Android", "iOS", "J2ME (Java ME)",
}

// aliases maps normalized keys (see key) of other spellings onto a canonical name.
var aliases = map[string]string{
	// Nintendo
	"nes": "Nintendo Entertainment System", "famicom": "Nintendo Entertainment System",
	"nintendo entertainment system (nes)": "Nintendo Entertainment System",
	"nintendo famicom":                    "Nintendo Entertainment System",
	"fds":                                 "Famicom Disk System",
	"snes":                                "Super Nintendo Entertainment System", "super nintendo": "Super Nintendo Entertainment System",
	"super nintendo entertainment system (snes)": "Super Nintendo Entertainment System",
	"super famicom":     "Super Nintendo Entertainment System",
	"n64":               "Nintendo 64",
	"nintendo gamecube": "GameCube", "game cube": "GameCube", "gc": "GameCube", "ngc": "GameCube",
	"nintendo wii": "Wii", "wiiu": "Wii U", "nintendo wii u": "Wii U",
	"switch": "Nintendo Switch", "nx": "Nintendo Switch", "nintendo switch 1": "Nintendo Switch",
	"switch 2": "Nintendo Switch 2", "switch2": "Nintendo Switch 2", "nintendo switch2": "Nintendo Switch 2",
	"nintendo switch (switch 2)": "Nintendo Switch 2", "nintendo switch (switch 2 edition)": "Nintendo Switch 2",
	"nintendo switch 2 edition": "Nintendo Switch 2", "ns2": "Nintendo Switch 2",
	"gb": "Game Boy", "gameboy": "Game Boy", "nintendo game boy": "Game Boy",
	"gbc": "Game Boy Color", "gameboy color": "Game Boy Color",
	"gba": "Game Boy Advance", "gameboy advance": "Game Boy Advance", "nintendo game boy advance": "Game Boy Advance",
	"nds": "Nintendo DS", "ds": "Nintendo DS", "nintendo dsi": "Nintendo DS",
	"3ds": "Nintendo 3DS", "n3ds": "Nintendo 3DS", "new nintendo 3ds": "Nintendo 3DS",
	// Sony
	"ps1": "PlayStation", "psx": "PlayStation", "playstation 1": "PlayStation", "playstation (ps1)": "PlayStation",
	"sony playstation": "PlayStation",
	"ps2":              "PlayStation 2", "playstation 2 (ps2)": "PlayStation 2",
	"ps3": "PlayStation 3", "playstation 3 (ps3)": "PlayStation 3",
	"ps4": "PlayStation 4", "playstation 4 (ps4)": "PlayStation 4",
	"ps5": "PlayStation 5", "playstation 5 (ps5)": "PlayStation 5",
	"psp": "PlayStation Portable", "playstation portable (psp)": "PlayStation Portable",
	"vita": "PlayStation Vita", "ps vita": "PlayStation Vita", "psvita": "PlayStation Vita",
	"xbox 360, playstation 3": "PlayStation 3, Xbox 360",
	// Microsoft
	"original xbox": "Xbox", "xbox (original)": "Xbox", "microsoft xbox": "Xbox", "xbox classic": "Xbox",
	"x360": "Xbox 360", "xbox360": "Xbox 360", "microsoft xbox 360": "Xbox 360",
	"xbox 360 (xbox live arcade)": "Xbox 360", "xbla": "Xbox 360",
	"xbox series x|s": "Xbox Series X/S", "xbox series x": "Xbox Series X/S", "xbox series s": "Xbox Series X/S",
	"xbox series": "Xbox Series X/S",
	// Sega
	"master system": "Sega Master System", "sms": "Sega Master System",
	"genesis": "Sega Genesis / Mega Drive", "mega drive": "Sega Genesis / Mega Drive",
	"sega genesis": "Sega Genesis / Mega Drive", "sega mega drive": "Sega Genesis / Mega Drive",
	"mega drive / genesis": "Sega Genesis / Mega Drive", "sega mega drive / genesis": "Sega Genesis / Mega Drive",
	"genesis / mega drive": "Sega Genesis / Mega Drive", "sega genesis/mega drive": "Sega Genesis / Mega Drive",
	"sega cd": "Mega-CD (Sega CD)", "mega-cd": "Mega-CD (Sega CD)", "mega cd": "Mega-CD (Sega CD)",
	"32x": "Sega 32X", "saturn": "Sega Saturn",
	"sega dreamcast": "Dreamcast", "dc": "Dreamcast",
	"naomi": "Sega NAOMI", "sega naomi (arcade)": "Sega NAOMI",
	"sega game gear": "Game Gear",
	// Other consoles
	"neo geo aes": "Neo Geo", "neo-geo": "Neo Geo", "ngpc": "Neo Geo Pocket Color",
	"turbografx-16": "PC Engine (TurboGrafx-16)", "pc engine": "PC Engine (TurboGrafx-16)",
	"cd-i": "Philips CD-i",
	// Arcade
	"cps2": "Arcade (CPS-2)", "cps-2": "Arcade (CPS-2)", "arcade (cps2)": "Arcade (CPS-2)",
	"arcade (capcom cps-2)": "Arcade (CPS-2)", "arcade (capcom cps2)": "Arcade (CPS-2)",
	"arcade (capcom play system ii / cps-2)": "Arcade (CPS-2)", "arcade (cp system ii)": "Arcade (CPS-2)",
	"cp system ii": "Arcade (CPS-2)", "cp system ii (cps-2)": "Arcade (CPS-2)",
	"cp system ii (capcom cps2 arcade)": "Arcade (CPS-2)", "cp system ii (arcade)": "Arcade (CPS-2)",
	"capcom cps-2": "Arcade (CPS-2)",
	"cps1":         "Arcade (CPS-1)", "cps-1": "Arcade (CPS-1)", "cps3": "Arcade (CPS-3)", "cps-3": "Arcade (CPS-3)",
	"sega model 2": "Arcade (Sega Model 2)", "sega model 2 (arcade)": "Arcade (Sega Model 2)",
	"sega model 3": "Arcade (Sega Model 3)", "sega model 3 (arcade)": "Arcade (Sega Model 3)",
	"sega lindbergh": "Arcade (Sega Lindbergh)", "sega lindbergh (arcade)": "Arcade (Sega Lindbergh)",
	"hyper neo geo 64 (arcade)": "Hyper Neo Geo 64",
	// DOS and Windows
	"dos": "MS-DOS", "msdos": "MS-DOS", "dos (ms-dos pc)": "MS-DOS", "dos (ms-dos)": "MS-DOS",
	"dos (pc)": "MS-DOS", "dos (x86 pc)": "MS-DOS", "ibm pc (dos)": "MS-DOS", "ms-dos (pc)": "MS-DOS",
	"ms-dos / pc": "MS-DOS", "pc (ms-dos)": "MS-DOS", "ms-dos/pc (build engine games)": "MS-DOS",
	"pc (dos)":    "MS-DOS",
	"dos/windows": "MS-DOS / Windows", "dos / windows": "MS-DOS / Windows", "ms-dos/windows": "MS-DOS / Windows",
	"ms-dos / microsoft windows": "MS-DOS / Windows", "pc (ms-dos/windows)": "MS-DOS / Windows",
	"pc (windows / ms-dos)": "MS-DOS / Windows", "pc (ms-dos / windows 95)": "MS-DOS / Windows",
	"microsoft windows": "Windows", "microsoft windows (pc)": "Windows", "microsoft windows (win32 x86)": "Windows",
	"windows (pc)": "Windows", "windows pc": "Windows", "pc (windows)": "Windows", "windows (32-bit)": "Windows",
	"windows (win32)": "Windows", "win32": "Windows", "pc (windows, steam release)": "Windows",
	"windows pc (gog)": "Windows", "pc windows": "Windows",
	"windows (pc), playstation 3, xbox 360":      "Windows, PlayStation 3, Xbox 360",
	"microsoft windows, playstation 3, xbox 360": "Windows, PlayStation 3, Xbox 360",
	// Other computers and mobile
	"commodore amiga": "Amiga", "c64": "Commodore 64",
	"pc-98": "NEC PC-98", "pc98": "NEC PC-98",
	"mac os x": "macOS", "os x": "macOS", "osx": "macOS", "macos (intel x86_64)": "macOS", "macos (i386)": "macOS",
	"android (armv7)": "Android", "android (arm64)": "Android",
	"ios (armv6)": "iOS", "ios (ipad)": "iOS", "ios (iphone/ipad)": "iOS", "iphone": "iOS",
	"j2me": "J2ME (Java ME)", "java me": "J2ME (Java ME)", "j2me (java me mobile)": "J2ME (Java ME)",
	"j2me (mobile java)": "J2ME (Java ME)", "java me (mobile)": "J2ME (Java ME)",
}

// canonicalByKey finds canonical names regardless of case and spacing.
var canonicalByKey = func() map[string]string {
	m := make(map[string]string, len(Canonical))
	for _, c := range Canonical {
		m[key(c)] = c
	}
	return m
}()

// key lowercases name, turns typographic hyphens into "-" and collapses spaces.
func key(name string) string {
	name = strings.NewReplacer("‐", "-", "‑", "-", "‒", "-", "–", "-", "—", "-").Replace(name)
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

// Normalize returns the canonical spelling of an original system, or the name
// trimmed of extra spaces if it isn't a known spelling.
func Normalize(name string) string {
	k := key(name)
	if c, ok := canonicalByKey[k]; ok {
		return c
	}
	if c, ok := aliases[k]; ok {
		return c
	}
	return strings.Join(strings.Fields(name), " ")
}
