// Package enrich fills, for a title the moment it's added, everything the
// catalog shows beyond its English record: Bulgarian / Greek / Spanish
// titles, descriptions and taglines, each language's poster / backdrop /
// logo / trailer (title_translations), the facts panel (title_facts) and,
// for series, episode names and descriptions (episode_translations), and
// the cast's names, biographies and places of birth (actor_translations).
//
// Title runs in the background after a repository insert, so adding a title
// never waits on (or fails because of) TMDB. Anything it misses is caught by
// tools/translations-backfill's sync, which fills the same tables.
//
// The same file lives in every service that adds titles: lumo-movies-svc,
// lumo-series-svc, lumo-movies-generator and lumo-movies-generator-by-actors.
package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var locales = []string{"bg", "el", "es"}

var tmdbLanguage = map[string]string{"bg": "bg-BG", "el": "el-GR", "es": "es-ES"}

var httpClient = &http.Client{Timeout: 20 * time.Second}

// Title enriches one title ("movie" or "tv") in the background.
func Title(pool *pgxpool.Pool, kind, tmdbID string) {
	if pool == nil || tmdbID == "" || os.Getenv("TMDB_API_KEY") == "" {
		return
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("enrich %s %s: panic: %v", kind, tmdbID, rec)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := run(ctx, pool, kind, tmdbID); err != nil {
			log.Printf("enrich %s %s: %v", kind, tmdbID, err)
		}
		// Then the cast: actors new to the catalog, in every language (actors.go).
		castActors(pool, kind, tmdbID)
	}()
}

func run(ctx context.Context, pool *pgxpool.Pool, kind, tmdbID string) error {
	var errs []error
	if err := translations(ctx, pool, kind, tmdbID); err != nil {
		errs = append(errs, fmt.Errorf("translations: %w", err))
	}
	if err := media(ctx, pool, kind, tmdbID); err != nil {
		errs = append(errs, fmt.Errorf("media: %w", err))
	}
	if err := facts(ctx, pool, kind, tmdbID); err != nil {
		errs = append(errs, fmt.Errorf("facts: %w", err))
	}
	if kind == "tv" {
		if err := episodes(ctx, pool, tmdbID); err != nil {
			errs = append(errs, fmt.Errorf("episodes: %w", err))
		}
	}
	return errors.Join(errs...)
}

// --- TMDB ---

var errNotFound = errors.New("not found on TMDB")

func get(ctx context.Context, path string, out any) error {
	key := os.Getenv("TMDB_API_KEY")
	endpoint := "https://api.themoviedb.org/3" + path
	bearer := len(key) > 60
	if !bearer {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		endpoint += sep + "api_key=" + url.QueryEscape(key)
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		if bearer {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := httpClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			return json.NewDecoder(resp.Body).Decode(out)
		}
		status := 0
		if resp != nil {
			status = resp.StatusCode
			resp.Body.Close()
		}
		if status == http.StatusNotFound {
			return errNotFound
		}
		if attempt >= 3 || ctx.Err() != nil {
			if err != nil {
				return err
			}
			return fmt.Errorf("TMDB %s: status %d", path, status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
}

func nonEmpty(s string) *string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return &s
}

// --- titles, descriptions, taglines ---

type translationsResponse struct {
	Translations []struct {
		Language string `json:"iso_639_1"`
		Country  string `json:"iso_3166_1"`
		Data     struct {
			Title    string `json:"title"`
			Name     string `json:"name"`
			Overview string `json:"overview"`
			Tagline  string `json:"tagline"`
		} `json:"data"`
	} `json:"translations"`
}

func translations(ctx context.Context, pool *pgxpool.Pool, kind, id string) error {
	var resp translationsResponse
	if err := get(ctx, fmt.Sprintf("/%s/%s/translations", kind, url.PathEscape(id)), &resp); err != nil && !errors.Is(err, errNotFound) {
		return err
	}
	preferredCountry := map[string]string{"bg": "BG", "el": "GR", "es": "ES"}
	batch := &pgx.Batch{}
	for _, locale := range locales {
		var title, description, tagline *string
		picked := false
		for _, t := range resp.Translations {
			if t.Language != locale || (picked && t.Country != preferredCountry[locale]) {
				continue
			}
			name := t.Data.Title
			if name == "" {
				name = t.Data.Name
			}
			title, description, tagline = nonEmpty(name), nonEmpty(t.Data.Overview), nonEmpty(t.Data.Tagline)
			picked = true
		}
		batch.Queue(`INSERT INTO title_translations (kind, tmdb_id, locale, title, description, special_text)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (kind, tmdb_id, locale) DO UPDATE
			SET title = EXCLUDED.title, description = EXCLUDED.description, special_text = EXCLUDED.special_text, updated_at = now()`,
			kind, id, locale, title, description, tagline)
	}
	return pool.SendBatch(ctx, batch).Close()
}

// --- each language's poster / backdrop / logo / trailer ---

type imageInfo struct {
	FilePath    string  `json:"file_path"`
	Language    string  `json:"iso_639_1"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
}

func bestImage(images []imageInfo, locale string) *string {
	var picked []imageInfo
	for _, image := range images {
		if image.Language == locale && image.FilePath != "" {
			picked = append(picked, image)
		}
	}
	if len(picked) == 0 {
		return nil
	}
	sort.SliceStable(picked, func(i, j int) bool {
		if picked[i].VoteAverage != picked[j].VoteAverage {
			return picked[i].VoteAverage > picked[j].VoteAverage
		}
		return picked[i].VoteCount > picked[j].VoteCount
	})
	return &picked[0].FilePath
}

type video struct {
	Key         string `json:"key"`
	Site        string `json:"site"`
	Type        string `json:"type"`
	Official    bool   `json:"official"`
	Language    string `json:"iso_639_1"`
	PublishedAt string `json:"published_at"`
}

func bestTrailer(videos []video, locale string) *string {
	rank := map[string]int{"Trailer": 0, "Teaser": 1}
	best := -1
	for i, v := range videos {
		if _, ok := rank[v.Type]; !ok || v.Language != locale || v.Site != "YouTube" || v.Key == "" {
			continue
		}
		if best < 0 {
			best = i
			continue
		}
		current := videos[best]
		switch {
		case v.Official != current.Official:
			if v.Official {
				best = i
			}
		case rank[v.Type] != rank[current.Type]:
			if rank[v.Type] < rank[current.Type] {
				best = i
			}
		case v.PublishedAt > current.PublishedAt:
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	link := "https://www.youtube.com/watch?v=" + videos[best].Key
	return &link
}

func media(ctx context.Context, pool *pgxpool.Pool, kind, id string) error {
	var images struct {
		Posters   []imageInfo `json:"posters"`
		Backdrops []imageInfo `json:"backdrops"`
		Logos     []imageInfo `json:"logos"`
	}
	if err := get(ctx, fmt.Sprintf("/%s/%s/images?include_image_language=bg,el,es", kind, url.PathEscape(id)), &images); err != nil && !errors.Is(err, errNotFound) {
		return err
	}
	var videos struct {
		Results []video `json:"results"`
	}
	if err := get(ctx, fmt.Sprintf("/%s/%s/videos?include_video_language=bg,el,es", kind, url.PathEscape(id)), &videos); err != nil && !errors.Is(err, errNotFound) {
		return err
	}
	batch := &pgx.Batch{}
	for _, locale := range locales {
		batch.Queue(`INSERT INTO title_translations (kind, tmdb_id, locale, poster_img_url, background_img_url, logo_url, trailer_url, media_checked)
			VALUES ($1, $2, $3, $4, $5, $6, $7, true)
			ON CONFLICT (kind, tmdb_id, locale) DO UPDATE
			SET poster_img_url = EXCLUDED.poster_img_url, background_img_url = EXCLUDED.background_img_url,
			    logo_url = EXCLUDED.logo_url, trailer_url = EXCLUDED.trailer_url, media_checked = true, updated_at = now()`,
			kind, id, locale, bestImage(images.Posters, locale), bestImage(images.Backdrops, locale),
			bestImage(images.Logos, locale), bestTrailer(videos.Results, locale))
	}
	return pool.SendBatch(ctx, batch).Close()
}

// --- facts panel ---

var featuredJobs = map[string]int{
	"Director": 0, "Creator": 1, "Screenplay": 2, "Writer": 3, "Story": 4,
	"Teleplay": 5, "Novel": 6, "Book": 7, "Author": 8, "Characters": 9, "Original Story": 10,
}

type crewMember struct {
	ID      int      `json:"id"`
	Name    string   `json:"name"`
	Jobs    []string `json:"jobs"`
	Profile string   `json:"profilePath,omitempty"` // TMDB photo path
}

type person struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Job     string `json:"job"`
	Profile string `json:"profile_path"`
}

func facts(ctx context.Context, pool *pgxpool.Pool, kind, id string) error {
	var resp struct {
		OriginalTitle    string   `json:"original_title"`
		OriginalName     string   `json:"original_name"`
		OriginalLanguage string   `json:"original_language"`
		Budget           int64    `json:"budget"`
		Revenue          int64    `json:"revenue"`
		Homepage         string   `json:"homepage"`
		CreatedBy        []person `json:"created_by"`
		Networks         []struct {
			Name string `json:"name"`
		} `json:"networks"`
		Credits struct {
			Crew []person `json:"crew"`
		} `json:"credits"`
		ExternalIDs struct {
			ImdbID      string `json:"imdb_id"`
			WikidataID  string `json:"wikidata_id"`
			FacebookID  string `json:"facebook_id"`
			InstagramID string `json:"instagram_id"`
			TwitterID   string `json:"twitter_id"`
			TiktokID    string `json:"tiktok_id"`
			YoutubeID   string `json:"youtube_id"`
		} `json:"external_ids"`
	}
	appendTo := "credits,external_ids"
	if kind == "tv" {
		appendTo = "external_ids"
	}
	if err := get(ctx, fmt.Sprintf("/%s/%s?append_to_response=%s", kind, url.PathEscape(id), appendTo), &resp); err != nil && !errors.Is(err, errNotFound) {
		return err
	}

	crew := []crewMember{}
	index := map[int]int{}
	add := func(p person, job string) {
		if i, ok := index[p.ID]; ok {
			for _, existing := range crew[i].Jobs {
				if existing == job {
					return
				}
			}
			crew[i].Jobs = append(crew[i].Jobs, job)
			return
		}
		index[p.ID] = len(crew)
		crew = append(crew, crewMember{ID: p.ID, Name: p.Name, Jobs: []string{job}, Profile: p.Profile})
	}
	for _, creator := range resp.CreatedBy {
		add(creator, "Creator")
	}
	for _, member := range resp.Credits.Crew {
		if _, featured := featuredJobs[member.Job]; featured {
			add(member, member.Job)
		}
	}
	rank := func(m crewMember) int {
		best := 99
		for _, job := range m.Jobs {
			if r := featuredJobs[job]; r < best {
				best = r
			}
		}
		return best
	}
	sort.SliceStable(crew, func(i, j int) bool { return rank(crew[i]) < rank(crew[j]) })
	if len(crew) > 8 {
		crew = crew[:8]
	}
	networks := []string{}
	for _, network := range resp.Networks {
		if name := strings.TrimSpace(network.Name); name != "" {
			networks = append(networks, name)
		}
	}
	crewJSON, _ := json.Marshal(crew)
	networksJSON, _ := json.Marshal(networks)
	originalTitle := resp.OriginalTitle
	if originalTitle == "" {
		originalTitle = resp.OriginalName
	}
	positive := func(n int64) *int64 {
		if n <= 0 {
			return nil
		}
		return &n
	}
	ext := resp.ExternalIDs
	_, err := pool.Exec(ctx, `
		INSERT INTO title_facts (kind, tmdb_id, original_title, original_language, budget, revenue, homepage,
			imdb_id, wikidata_id, facebook_id, instagram_id, twitter_id, tiktok_id, youtube_id, crew, networks)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (kind, tmdb_id) DO UPDATE SET
			original_title = EXCLUDED.original_title, original_language = EXCLUDED.original_language,
			budget = EXCLUDED.budget, revenue = EXCLUDED.revenue, homepage = EXCLUDED.homepage,
			imdb_id = EXCLUDED.imdb_id, wikidata_id = EXCLUDED.wikidata_id, facebook_id = EXCLUDED.facebook_id,
			instagram_id = EXCLUDED.instagram_id, twitter_id = EXCLUDED.twitter_id, tiktok_id = EXCLUDED.tiktok_id,
			youtube_id = EXCLUDED.youtube_id, crew = EXCLUDED.crew, networks = EXCLUDED.networks, updated_at = now()`,
		kind, id, nonEmpty(originalTitle), nonEmpty(resp.OriginalLanguage), positive(resp.Budget), positive(resp.Revenue),
		nonEmpty(resp.Homepage), nonEmpty(ext.ImdbID), nonEmpty(ext.WikidataID), nonEmpty(ext.FacebookID),
		nonEmpty(ext.InstagramID), nonEmpty(ext.TwitterID), nonEmpty(ext.TiktokID), nonEmpty(ext.YoutubeID),
		string(crewJSON), string(networksJSON))
	return err
}

// --- series: episode names / descriptions ---

var genericEpisodeName = regexp.MustCompile(`(?i)^(епизод|επεισόδιο|episodio|episode)\s*\d+$`)

// episodes translates the series' seasons that have no translations yet
// (a brand-new series, or a season just added by the new-episodes check).
func episodes(ctx context.Context, pool *pgxpool.Pool, seriesID string) error {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT e.season FROM episodes e JOIN series s ON s.id = e.series_id
		WHERE s.tmdb_id = $1 AND NULLIF(e.season, '') IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM episode_translations t WHERE t.series_tmdb_id = $1 AND t.season = e.season)`, seriesID)
	if err != nil {
		return err
	}
	seasons, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, season := range seasons {
		batch := &pgx.Batch{}
		for _, locale := range locales {
			var resp struct {
				Episodes []struct {
					Number   int    `json:"episode_number"`
					Name     string `json:"name"`
					Overview string `json:"overview"`
				} `json:"episodes"`
			}
			path := fmt.Sprintf("/tv/%s/season/%s?language=%s", url.PathEscape(seriesID), url.PathEscape(season), tmdbLanguage[locale])
			if err := get(ctx, path, &resp); err != nil {
				if errors.Is(err, errNotFound) {
					continue
				}
				return err
			}
			for _, episode := range resp.Episodes {
				name := episode.Name
				if genericEpisodeName.MatchString(strings.TrimSpace(name)) {
					name = ""
				}
				batch.Queue(`INSERT INTO episode_translations (series_tmdb_id, season, episode, locale, title, description)
					VALUES ($1, $2, $3, $4, $5, $6)
					ON CONFLICT (series_tmdb_id, season, episode, locale) DO UPDATE
					SET title = EXCLUDED.title, description = EXCLUDED.description, updated_at = now()`,
					seriesID, season, fmt.Sprint(episode.Number), locale, nonEmpty(name), nonEmpty(episode.Overview))
			}
		}
		if batch.Len() == 0 {
			batch.Queue(`INSERT INTO episode_translations (series_tmdb_id, season, episode, locale) VALUES ($1, $2, '0', 'bg')
				ON CONFLICT DO NOTHING`, seriesID, season)
		}
		if err := pool.SendBatch(ctx, batch).Close(); err != nil {
			return err
		}
	}
	return nil
}
