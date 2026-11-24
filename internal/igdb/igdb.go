// Package igdb finds the IGDB (igdb.com) pages of games and platforms, so the
// list can link "Super Mario 64" and "Nintendo 64" to their entries.
//
// It talks to the IGDB v4 API directly: only two endpoints are needed, and
// access tokens come from Twitch's client-credentials flow, renewed when they
// expire.
package igdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultAPIURL   = "https://api.igdb.com/v4/"
	defaultTokenURL = "https://id.twitch.tv/oauth2/token"
	// minInterval keeps requests under IGDB's limit of 4 per second.
	minInterval = 260 * time.Millisecond
	// platformsTTL is how long the platform list is reused before reloading it.
	platformsTTL = 24 * time.Hour
)

// Platform is an IGDB platform (console, computer, arcade...).
type Platform struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	URL             string `json:"url"`
	Abbreviation    string `json:"abbreviation"`
	AlternativeName string `json:"alternative_name"`
}

// Link is the platform's IGDB page.
func (p Platform) Link() string {
	if p.URL != "" {
		return p.URL
	}
	return "https://www.igdb.com/platforms/" + p.Slug
}

// Game is an IGDB game search result.
type Game struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	Slug             string `json:"slug"`
	URL              string `json:"url"`
	Platforms        []int  `json:"platforms"`
	AlternativeNames []struct {
		Name string `json:"name"`
	} `json:"alternative_names"`
}

// Link is the game's IGDB page.
func (g Game) Link() string {
	if g.URL != "" {
		return g.URL
	}
	return "https://www.igdb.com/games/" + g.Slug
}

// Client calls the IGDB API with Twitch app credentials.
type Client struct {
	clientID, clientSecret string
	apiURL, tokenURL       string
	http                   *http.Client

	tokenMu sync.Mutex
	token   string
	expires time.Time

	rateMu sync.Mutex
	last   time.Time

	platMu     sync.Mutex
	platforms  []Platform
	platLoaded time.Time
}

// New returns a client for the Twitch application with these credentials.
func New(clientID, clientSecret string) *Client {
	return &Client{
		clientID: clientID, clientSecret: clientSecret,
		apiURL: defaultAPIURL, tokenURL: defaultTokenURL,
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

// accessToken returns a valid app access token, fetching a new one if needed.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}
	form := url.Values{"client_id": {c.clientID}, "client_secret": {c.clientSecret}, "grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("getting IGDB access token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("getting IGDB access token: %s: %s", resp.Status, snippet(resp.Body))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("decoding IGDB access token: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("getting IGDB access token: empty token in response")
	}
	c.token = tok.AccessToken
	c.expires = time.Now().Add(time.Duration(tok.ExpiresIn)*time.Second - time.Minute)
	return c.token, nil
}

// wait spaces out requests to respect the rate limit.
func (c *Client) wait(ctx context.Context) error {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	if d := minInterval - time.Since(c.last); d > 0 {
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	c.last = time.Now()
	return nil
}

// query posts an Apicalypse query to endpoint and decodes the JSON result into out.
func (c *Client) query(ctx context.Context, endpoint, body string, out any) error {
	for attempt := 0; ; attempt++ {
		token, err := c.accessToken(ctx)
		if err != nil {
			return err
		}
		if err := c.wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+endpoint, bytes.NewBufferString(body))
		if err != nil {
			return err
		}
		req.Header.Set("Client-ID", c.clientID)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("IGDB %s: %w", endpoint, err)
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			// The token was revoked or expired early: get a new one and retry once.
			resp.Body.Close()
			c.tokenMu.Lock()
			c.token = ""
			c.tokenMu.Unlock()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("IGDB %s: %s: %s", endpoint, resp.Status, snippet(resp.Body))
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decoding IGDB %s: %w", endpoint, err)
		}
		return nil
	}
}

func snippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 300))
	return strings.TrimSpace(string(b))
}

// Platforms returns every IGDB platform, cached for a day.
func (c *Client) Platforms(ctx context.Context) ([]Platform, error) {
	c.platMu.Lock()
	defer c.platMu.Unlock()
	if c.platforms != nil && time.Since(c.platLoaded) < platformsTTL {
		return c.platforms, nil
	}
	var all []Platform
	for offset := 0; ; offset += 500 {
		var page []Platform
		q := "fields id,name,slug,url,abbreviation,alternative_name; sort id asc; limit 500; offset " + strconv.Itoa(offset) + ";"
		if err := c.query(ctx, "platforms", q, &page); err != nil {
			if c.platforms != nil {
				return c.platforms, nil // keep using the old list
			}
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 500 {
			break
		}
	}
	c.platforms, c.platLoaded = all, time.Now()
	return all, nil
}

// PlatformURL returns the IGDB page of an original system such as "PlayStation 2",
// or "" if it doesn't match one IGDB platform (unknown, or several systems).
func (c *Client) PlatformURL(ctx context.Context, system string) (string, error) {
	all, err := c.Platforms(ctx)
	if err != nil {
		return "", err
	}
	plats, single := matchSystem(all, system)
	if !single || len(plats) == 0 {
		return "", nil
	}
	return plats[0].Link(), nil
}

// GameURL returns the IGDB page of game as released on system, or "" if no
// IGDB game matches well enough.
func (c *Client) GameURL(ctx context.Context, game, system string) (string, error) {
	game = strings.TrimSpace(game)
	if game == "" {
		return "", nil
	}
	all, err := c.Platforms(ctx)
	if err != nil {
		return "", err
	}
	plats, _ := matchSystem(all, system)
	var ids []string
	for _, p := range plats {
		ids = append(ids, strconv.Itoa(p.ID))
	}
	search := func(where string) ([]Game, error) {
		q := fmt.Sprintf(`search "%s"; fields name,slug,url,platforms,alternative_names.name; %slimit 25;`, escape(game), where)
		var games []Game
		return games, c.query(ctx, "games", q, &games)
	}
	if len(ids) > 0 {
		games, err := search("where platforms = (" + strings.Join(ids, ",") + "); ")
		if err != nil {
			return "", err
		}
		if g := bestGame(games, game, false); g != nil {
			return g.Link(), nil
		}
	}
	// Unknown system, or IGDB doesn't list the game on it: accept only an exact title.
	games, err := search("")
	if err != nil {
		return "", err
	}
	if g := bestGame(games, game, true); g != nil {
		return g.Link(), nil
	}
	return "", nil
}

// escape makes s safe inside a double-quoted Apicalypse string.
func escape(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// bestGame picks the search result whose title matches name best: an exact
// title (or alternative title) first, then, unless exactOnly, one whose title
// contains name or is contained in it. Ties keep IGDB's relevance order.
func bestGame(games []Game, name string, exactOnly bool) *Game {
	want := key(name)
	if want == "" {
		return nil
	}
	var best *Game
	bestScore := 0
	for i := range games {
		g := &games[i]
		score := 0
		titles := []string{g.Name}
		for _, a := range g.AlternativeNames {
			titles = append(titles, a.Name)
		}
		for _, t := range titles {
			k := key(t)
			switch {
			case k == "":
			case k == want:
				score = max(score, 2)
			case !exactOnly && (strings.Contains(k, want) || strings.Contains(want, k)):
				score = max(score, 1)
			}
		}
		if score > bestScore {
			best, bestScore = g, score
		}
	}
	return best
}
