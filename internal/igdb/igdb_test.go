package igdb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"

	"github.com/ricardo-duarte-av/recomps-ai-bot/internal/store"
)

var testPlatforms = []Platform{
	{ID: 8, Name: "PlayStation 2", Slug: "ps2", URL: "https://www.igdb.com/platforms/ps2", Abbreviation: "PS2"},
	{ID: 41, Name: "Wii U", Slug: "wiiu", URL: "https://www.igdb.com/platforms/wiiu", Abbreviation: "WiiU"},
	{ID: 130, Name: "Nintendo Switch", Slug: "switch", URL: "https://www.igdb.com/platforms/switch", Abbreviation: "Switch"},
	{ID: 21, Name: "Nintendo GameCube", Slug: "ngc", URL: "https://www.igdb.com/platforms/ngc", Abbreviation: "NGC"},
	{ID: 29, Name: "Sega Mega Drive/Genesis", Slug: "genesis-slash-megadrive", Abbreviation: "Genesis/MegaDrive"},
	{ID: 52, Name: "Arcade", Slug: "arcade", URL: "https://www.igdb.com/platforms/arcade"},
	{ID: 6, Name: "PC (Microsoft Windows)", Slug: "win", URL: "https://www.igdb.com/platforms/win", Abbreviation: "PC"},
	{ID: 13, Name: "DOS", Slug: "dos", URL: "https://www.igdb.com/platforms/dos", Abbreviation: "DOS"},
	{ID: 9, Name: "PlayStation 3", Slug: "ps3", URL: "https://www.igdb.com/platforms/ps3", Abbreviation: "PS3"},
	{ID: 12, Name: "Xbox 360", Slug: "xbox360", URL: "https://www.igdb.com/platforms/xbox360", Abbreviation: "X360"},
	{ID: 49, Name: "Xbox One", Slug: "xboxone", Abbreviation: "XONE"},
	{ID: 169, Name: "Xbox Series X|S", Slug: "series-x-s", Abbreviation: "Series X|S"},
}

func TestMatchSystem(t *testing.T) {
	for _, tc := range []struct {
		system string
		ids    []int
		single bool
	}{
		{"PlayStation 2", []int{8}, true},
		{"ps2", []int{8}, true},
		{"GameCube", []int{21}, true},
		{"Sega Genesis / Mega Drive", []int{29}, true},
		{"Arcade (CPS-2)", []int{52}, true},
		{"MS-DOS / Windows", []int{13, 6}, true},
		{"Xbox Series X/S", []int{169}, true},
		{"Windows, PlayStation 3, Xbox 360", []int{6, 9, 12}, false},
		{"PlayStation 5 / Xbox Series X/S", []int{169}, true},
		{"Amiga", nil, false},
	} {
		plats, single := matchSystem(testPlatforms, tc.system)
		var ids []int
		for _, p := range plats {
			ids = append(ids, p.ID)
		}
		if !equalInts(ids, tc.ids) || single != tc.single {
			t.Errorf("matchSystem(%q) = %v, %v; want %v, %v", tc.system, ids, single, tc.ids, tc.single)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fakeIGDB serves a token endpoint and the platforms and games endpoints.
func fakeIGDB(t *testing.T, games func(query string) []Game) (*Client, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.FormValue("client_id") != "id" || r.FormValue("client_secret") != "secret" {
				http.Error(w, "bad credentials", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Client-ID") != "id" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v4/platforms":
			json.NewEncoder(w).Encode(testPlatforms)
		case "/v4/games":
			json.NewEncoder(w).Encode(games(string(body)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := New("id", "secret")
	c.apiURL, c.tokenURL = srv.URL+"/v4/", srv.URL+"/token"
	return c, &calls
}

func TestGameURL(t *testing.T) {
	c, _ := fakeIGDB(t, func(q string) []Game {
		switch {
		case strings.Contains(q, `search "Super Mario 3D World"`) && strings.Contains(q, "where platforms = (41)"):
			return []Game{{Name: "Super Mario 3D World", Slug: "super-mario-3d-world", URL: "https://www.igdb.com/games/super-mario-3d-world", Platforms: []int{41}}}
		case strings.Contains(q, `search "Super Mario 3D World"`) && strings.Contains(q, "where platforms = (130)"):
			return []Game{{Name: "Super Mario 3D World + Bowser's Fury", Slug: "super-mario-3d-world-plus-bowsers-fury", Platforms: []int{130}}}
		case strings.Contains(q, `search "Prototype"`):
			return []Game{
				{ID: 235105, Name: "Prototype", Slug: "prototype--2", Platforms: []int{6}},
				{ID: 296161, Name: "Prototype", Slug: "prototype-rerelease", Platforms: []int{6}, VersionParent: 570, TotalRatingCount: 900},
				{ID: 570, Name: "Prototype", Slug: "prototype", Platforms: []int{6}, TotalRatingCount: 400},
			}
		case strings.Contains(q, `search "Serious Sam Classic"`):
			return []Game{{Name: "Serious Sam Classic", Slug: "serious-sam-classic"}}
		case strings.Contains(q, `search "Pokémon Scarlet"`):
			return []Game{{Name: "Pokémon Scarlet", Slug: "pokemon-scarlet"}}
		case strings.Contains(q, `search "Unknown Game"`):
			return []Game{{Name: "Unknown Game Deluxe", Slug: "ugd"}}
		}
		return nil
	})
	ctx := context.Background()
	for _, tc := range []struct{ game, system, want string }{
		{"Super Mario 3D World", "Wii U", "https://www.igdb.com/games/super-mario-3d-world"},
		{"Super Mario 3D World", "Nintendo Switch", "https://www.igdb.com/games/super-mario-3d-world-plus-bowsers-fury"},
		{"Prototype", "Windows", "https://www.igdb.com/games/prototype"},
		{"Serious Sam Classic (The First Encounter / The Second Encounter)", "Amiga", "https://www.igdb.com/games/serious-sam-classic"},
		{"Pokémon Scarlet & Violet", "Nintendo Switch", "https://www.igdb.com/games/pokemon-scarlet"},
		// Unknown system: only an exact title is accepted.
		{"Unknown Game", "Amiga", ""},
	} {
		got, err := c.GameURL(ctx, tc.game, tc.system)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("GameURL(%q, %q) = %q, want %q", tc.game, tc.system, got, tc.want)
		}
	}
	got, err := c.PlatformURL(ctx, "PS2")
	if err != nil || got != "https://www.igdb.com/platforms/ps2" {
		t.Errorf("PlatformURL(PS2) = %q, %v", got, err)
	}
}

func TestEscape(t *testing.T) {
	if got := escape("Say \"Hi\"\n\\ now"); got != `Say \"Hi\" \\ now` {
		t.Errorf("escape = %q", got)
	}
}

func TestLinkerCaches(t *testing.T) {
	c, calls := fakeIGDB(t, func(q string) []Game {
		return []Game{{ID: 77, Name: "Street Fighter Alpha Anthology", Slug: "street-fighter-alpha-anthology",
			URL: "https://www.igdb.com/games/street-fighter-alpha-anthology", Platforms: []int{8}}}
	})
	st, err := store.Open(filepath.Join(t.TempDir(), "igdb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	l := NewLinker(c, st, zerolog.Nop())
	ctx := context.Background()
	for range 2 {
		g, s := l.Links(ctx, "Street Fighter Alpha Anthology", "PlayStation 2")
		if g != "https://www.igdb.com/games/street-fighter-alpha-anthology" || s != "https://www.igdb.com/platforms/ps2" {
			t.Fatalf("Links = %q, %q", g, s)
		}
	}
	if n := calls.Load(); n != 2 { // platforms once, games once; the second round is cached
		t.Errorf("IGDB was called %d times, want 2", n)
	}
	links, err := st.IGDBLinks(ctx)
	if err != nil || links[store.IGDBGameKey("Street Fighter Alpha Anthology", "PlayStation 2")].ID != 77 {
		t.Errorf("IGDBLinks = %v, %v", links, err)
	}

	var nilLinker *Linker
	if g, s := nilLinker.Links(ctx, "x", "y"); g != "" || s != "" {
		t.Error("nil linker returned links")
	}
	nilLinker.Kick()
}

func TestImageURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		body, _ := io.ReadAll(r.Body)
		q := string(body)
		switch {
		case strings.Contains(q, `slug = "with-cover"`):
			w.Write([]byte(`[{"cover":{"image_id":"co1"},"screenshots":[{"image_id":"sc1"}]}]`))
		case strings.Contains(q, `slug = "edition"`):
			w.Write([]byte(`[{"version_parent":{"cover":{"image_id":"co2"}},"screenshots":[{"image_id":"sc2"}]}]`))
		case strings.Contains(q, `slug = "art-only"`):
			w.Write([]byte(`[{"artworks":[{"image_id":"ar3"}],"screenshots":[{"image_id":"sc3"}]}]`))
		case strings.Contains(q, `slug = "shots-only"`):
			w.Write([]byte(`[{"screenshots":[{"image_id":"sc4"}]}]`))
		case strings.Contains(q, `slug = "ps2"`):
			w.Write([]byte(`[{"platform_logo":{"image_id":"pl5"}}]`))
		default:
			w.Write([]byte(`[{}]`))
		}
	}))
	t.Cleanup(srv.Close)
	c := New("id", "secret")
	c.apiURL, c.tokenURL = srv.URL+"/v4/", srv.URL+"/token"
	for page, want := range map[string]string{
		"https://www.igdb.com/games/with-cover":   "https://images.igdb.com/igdb/image/upload/t_cover_big/co1.jpg",
		"https://www.igdb.com/games/edition":      "https://images.igdb.com/igdb/image/upload/t_cover_big/co2.jpg",
		"https://www.igdb.com/games/art-only":     "https://images.igdb.com/igdb/image/upload/t_screenshot_med/ar3.jpg",
		"https://www.igdb.com/games/shots-only":   "https://images.igdb.com/igdb/image/upload/t_screenshot_med/sc4.jpg",
		"https://www.igdb.com/games/nothing":      "",
		"https://www.igdb.com/platforms/ps2":      "https://images.igdb.com/igdb/image/upload/t_logo_med/pl5.png",
		"https://www.igdb.com/something/else/too": "",
	} {
		got, err := c.ImageURL(context.Background(), page)
		if err != nil || got != want {
			t.Errorf("ImageURL(%s) = %q, %v; want %q", page, got, err, want)
		}
	}
}

// TestLinkerCovers checks that sync stores game IDs (also for links cached
// before IDs were), caches covers once, and serves announcement images from
// the cache.
func TestLinkerCovers(t *testing.T) {
	var images atomic.Int32
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		q := string(body)
		switch {
		case r.URL.Path == "/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
		case r.URL.Path == "/v4/platforms":
			json.NewEncoder(w).Encode(testPlatforms)
		case strings.HasPrefix(r.URL.Path, "/img/"):
			images.Add(1)
			w.Write([]byte("\x89PNG\r\n\x1a\n fake"))
		case strings.Contains(q, `search "New Game"`):
			w.Write([]byte(`[{"id":5,"name":"New Game","slug":"new-game","platforms":[8]}]`))
		case strings.Contains(q, `fields id; where slug = "old-game"`):
			w.Write([]byte(`[{"id":6}]`))
		case strings.Contains(q, "where id = 5"):
			w.Write([]byte(`[{"cover":{"image_id":"co5"}}]`))
		case strings.Contains(q, "where id = 6"):
			w.Write([]byte(`[{}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(srv.Close)
	srvURL = srv.URL
	c := New("id", "secret")
	c.apiURL, c.tokenURL = srvURL+"/v4/", srvURL+"/token"
	st, err := store.Open(filepath.Join(t.TempDir(), "covers.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	for _, e := range []store.Entry{
		{RepoURL: "https://github.com/a/new", Game: "New Game", OriginalSystem: "PlayStation 2", Kind: "recomp", OSes: []string{"linux"}},
		{RepoURL: "https://github.com/a/old", Game: "Old Game", OriginalSystem: "PlayStation 2", Kind: "recomp", OSes: []string{"linux"}},
	} {
		if err := st.Add(ctx, e, "@mod:x"); err != nil {
			t.Fatal(err)
		}
	}
	// Linked before IDs were stored.
	if err := st.SetIGDBLink(ctx, store.IGDBGameKey("Old Game", "PlayStation 2"), "https://www.igdb.com/games/old-game", 0); err != nil {
		t.Fatal(err)
	}
	l := NewLinker(c, st, zerolog.Nop())
	// Point image downloads at the fake server.
	l.client.http.Transport = rewriteImages{srvURL}
	for range 2 {
		l.sync(ctx)
	}
	links, _ := st.IGDBLinks(ctx)
	if id := links[store.IGDBGameKey("New Game", "PlayStation 2")].ID; id != 5 {
		t.Errorf("new game ID = %d, want 5", id)
	}
	if id := links[store.IGDBGameKey("Old Game", "PlayStation 2")].ID; id != 6 {
		t.Errorf("old game ID = %d, want 6", id)
	}
	ids, err := st.IGDBCoverIDs(ctx)
	if err != nil || !ids[5] || ids[6] {
		t.Errorf("cover IDs = %v, %v; want only 5", ids, err)
	}
	if _, ok, _ := st.IGDBCover(ctx, 6); !ok {
		t.Error("missing cover of game 6 wasn't recorded")
	}
	img, err := l.Image(ctx, "https://www.igdb.com/games/new-game")
	if err != nil || img == nil || img.MimeType != "image/png" {
		t.Fatalf("Image = %+v, %v", img, err)
	}
	if n := images.Load(); n != 1 {
		t.Errorf("downloaded %d images, want 1", n)
	}
}

// rewriteImages sends requests for images.igdb.com to a test server.
type rewriteImages struct{ base string }

func (rw rewriteImages) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "images.igdb.com" {
		u, _ := url.Parse(rw.base + "/img" + r.URL.Path)
		r = r.Clone(r.Context())
		r.URL, r.Host = u, u.Host
	}
	return http.DefaultTransport.RoundTrip(r)
}
