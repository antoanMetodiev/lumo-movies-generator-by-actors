package enrich

// Actor translations at insert time: once a title is added, every actor of
// its cast that has no actor_translations rows yet gets their name, place of
// birth and biography in Bulgarian, Greek and Spanish. TMDB's own
// translations come first; Gemini (GEMINI_API_KEY) translates the rest in one
// request per actor. No key, or no actor_translations table, skips it — the
// sync in tools/translations-backfill (same logic) catches anything missed.
//
// Same file in lumo-movies-svc, lumo-series-svc, lumo-movies-generator and
// lumo-movies-generator-by-actors.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const geminiModel = "gemini-3.5-flash-lite"

var geminiHTTP = &http.Client{Timeout: 3 * time.Minute}

// geminiSlots caps this process's Gemini requests in flight — a generator
// adding a batch of titles would otherwise fire hundreds at once.
var geminiSlots = make(chan struct{}, 2)

type actorRow struct {
	imdbID, name, biography, placeOfBirth string
}

type actorFields struct {
	Name         string `json:"name,omitempty"`
	PlaceOfBirth string `json:"placeOfBirth,omitempty"`
	Biography    string `json:"biography,omitempty"`
}

// castActors translates the untranslated actors of one title.
func castActors(pool *pgxpool.Pool, kind, tmdbID string) {
	if os.Getenv("GEMINI_API_KEY") == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	query := `
		SELECT DISTINCT ON (a.imdb_id) a.imdb_id, coalesce(a.name_in_real_life, ''), coalesce(a.biography, ''), coalesce(a.place_of_birth, '')
		FROM movies m JOIN movies_actors ma ON ma.movie_id = m.id JOIN actors a ON a.id = ma.actor_id
		WHERE m.tmdb_id = $1 AND NULLIF(a.imdb_id, '') IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM actor_translations t WHERE t.imdb_id = a.imdb_id)`
	if kind == "tv" {
		query = strings.NewReplacer("movies m", "series s", "movies_actors ma", "series_actors ma",
			"ma.movie_id = m.id", "ma.series_id = s.id", "m.tmdb_id", "s.tmdb_id").Replace(query)
	}
	rows, err := pool.Query(ctx, query, tmdbID)
	if err != nil {
		if !strings.Contains(err.Error(), "actor_translations") { // table not created yet
			log.Printf("enrich actors %s %s: %v", kind, tmdbID, err)
		}
		return
	}
	actors, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (actorRow, error) {
		var a actorRow
		err := row.Scan(&a.imdbID, &a.name, &a.biography, &a.placeOfBirth)
		return a, err
	})
	if err != nil || len(actors) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, a := range actors {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := translateActor(ctx, pool, a); err != nil {
				log.Printf("enrich actor %s: %v", a.imdbID, err)
			}
		}()
	}
	wg.Wait()
}

type personResponse struct {
	AlsoKnownAs  []string `json:"also_known_as"`
	Translations struct {
		Translations []struct {
			Language string `json:"iso_639_1"`
			Country  string `json:"iso_3166_1"`
			Data     struct {
				Name      string `json:"name"`
				Biography string `json:"biography"`
			} `json:"data"`
		} `json:"translations"`
	} `json:"translations"`
}

func translateActor(ctx context.Context, pool *pgxpool.Pool, a actorRow) error {
	// TMDB first: a translated biography / name where TMDB has one.
	have := map[string]actorFields{}
	var found struct {
		PersonResults []struct {
			ID int `json:"id"`
		} `json:"person_results"`
	}
	if err := get(ctx, fmt.Sprintf("/find/%s?external_source=imdb_id", url.PathEscape(a.imdbID)), &found); err != nil && !errors.Is(err, errNotFound) {
		return err
	}
	if len(found.PersonResults) > 0 {
		var person personResponse
		err := get(ctx, fmt.Sprintf("/person/%d?append_to_response=translations", found.PersonResults[0].ID), &person)
		if err != nil && !errors.Is(err, errNotFound) {
			return err
		}
		preferred := map[string]string{"bg": "BG", "el": "GR", "es": "ES"}
		for _, locale := range locales {
			var f actorFields
			for _, t := range person.Translations.Translations {
				if t.Language != locale || (f.Biography != "" && t.Country != preferred[locale]) {
					continue
				}
				if b := strings.TrimSpace(t.Data.Biography); b != "" {
					f.Biography = b
				}
				if n := strings.TrimSpace(t.Data.Name); n != "" && inScript(n, locale) {
					f.Name = n
				}
			}
			// Greek-letter aliases are safe; Cyrillic ones are usually Russian.
			if f.Name == "" && locale == "el" {
				for _, alias := range person.AlsoKnownAs {
					if inScript(alias, "el") {
						f.Name = strings.TrimSpace(alias)
						break
					}
				}
			}
			have[locale] = f
		}
	}

	need := map[string][]string{}
	for _, locale := range locales {
		var fields []string
		if have[locale].Name == "" && a.name != "" {
			fields = append(fields, "name")
		}
		if strings.TrimSpace(a.placeOfBirth) != "" {
			fields = append(fields, "placeOfBirth")
		}
		if have[locale].Biography == "" && strings.TrimSpace(a.biography) != "" {
			fields = append(fields, "biography")
		}
		if len(fields) > 0 {
			need[locale] = fields
		}
	}

	got := map[string]actorFields{}
	if len(need) > 0 {
		var err error
		if got, err = geminiTranslate(ctx, a, need); err != nil {
			return err
		}
		for locale, fields := range need {
			for attempt := 0; attempt < 3 && broken(got[locale], fields, locale); attempt++ {
				again, err := geminiTranslate(ctx, a, map[string][]string{locale: fields})
				if err != nil {
					return err
				}
				got[locale] = again[locale]
			}
			if broken(got[locale], fields, locale) {
				return fmt.Errorf("gemini kept returning a broken %s translation", locale)
			}
		}
	}

	batch := &pgx.Batch{}
	for _, locale := range locales {
		name := firstNonEmpty(have[locale].Name, got[locale].Name, a.name)
		biography := firstNonEmpty(have[locale].Biography, got[locale].Biography)
		batch.Queue(`INSERT INTO actor_translations (imdb_id, locale, name, biography, place_of_birth)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (imdb_id, locale) DO UPDATE
			SET name = EXCLUDED.name, biography = EXCLUDED.biography, place_of_birth = EXCLUDED.place_of_birth, updated_at = now()`,
			a.imdbID, locale, nonEmpty(name), nonEmpty(biography), nonEmpty(got[locale].PlaceOfBirth))
	}
	return pool.SendBatch(ctx, batch).Close()
}

// broken: a needed field missing, or a letter of a foreign alphabet in it
// (writing three languages at once, the model now and then mixes them).
func broken(f actorFields, fields []string, locale string) bool {
	for _, field := range fields {
		if (field == "name" && f.Name == "") || (field == "placeOfBirth" && f.PlaceOfBirth == "") || (field == "biography" && f.Biography == "") {
			return true
		}
	}
	foreign := map[string][]*unicode.RangeTable{
		"bg": {unicode.Greek},
		"el": {unicode.Cyrillic},
		"es": {unicode.Greek, unicode.Cyrillic},
	}[locale]
	for _, r := range f.Name + f.PlaceOfBirth + f.Biography {
		if unicode.IsLetter(r) && unicode.In(r, foreign...) {
			return true
		}
	}
	return false
}

// inScript: written in the locale's alphabet (Cyrillic / Greek / Latin).
func inScript(s, locale string) bool {
	table := map[string]*unicode.RangeTable{"bg": unicode.Cyrillic, "el": unicode.Greek, "es": unicode.Latin}[locale]
	letters, matching := 0, 0
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if unicode.Is(table, r) {
			matching++
		}
	}
	return letters > 0 && matching*10 >= letters*8
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

const geminiActorPrompt = `You translate actor profiles from English for a movie streaming website.
You get one actor's English name, place of birth and biography, and for each target language the list of fields to produce ("need").
Return JSON with one object per language listed in "need", holding exactly the needed fields.

Rules:
- name: the person's name as that language's media write it. Bulgarian: Cyrillic, Bulgarian transcription (Robert Pattinson → Робърт Патинсън, Leonardo DiCaprio → Леонардо ди Каприо), never the Russian spelling. Greek: Greek letters (Ρόμπερτ Πάτινσον). Spanish: the original name in Latin letters.
- placeOfBirth: translate every place name, keep the comma-separated order (London, England, UK → Лондон, Англия, Обединеното кралство).
- biography: a complete, faithful, natural translation. Keep every paragraph break. Do not add, shorten or summarise. Person and title names inside the text follow the same spelling rules (film titles: their known title in that language when there is one).
- Output only the JSON.`

func geminiTranslate(ctx context.Context, a actorRow, need map[string][]string) (map[string]actorFields, error) {
	select {
	case geminiSlots <- struct{}{}:
		defer func() { <-geminiSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	input, _ := json.Marshal(map[string]any{"name": a.name, "placeOfBirth": a.placeOfBirth, "biography": a.biography, "need": need})
	fields := map[string]any{"type": "OBJECT", "properties": map[string]any{
		"name":         map[string]any{"type": "STRING"},
		"placeOfBirth": map[string]any{"type": "STRING"},
		"biography":    map[string]any{"type": "STRING"},
	}}
	properties := map[string]any{}
	for locale := range need {
		properties[locale] = fields
	}
	body, _ := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": geminiActorPrompt}}},
		"contents":          []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": string(input)}}}},
		"generationConfig": map[string]any{
			"temperature":      0.2,
			"maxOutputTokens":  32768,
			"responseMimeType": "application/json",
			"responseSchema":   map[string]any{"type": "OBJECT", "properties": properties},
			"thinkingConfig":   map[string]any{"thinkingLevel": "minimal"},
		},
	})

	endpoint := "https://generativelanguage.googleapis.com/v1beta/models/" + geminiModel + ":generateContent"
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", os.Getenv("GEMINI_API_KEY"))
		resp, err := geminiHTTP.Do(req)
		status := 0
		var raw []byte
		if err == nil {
			status = resp.StatusCode
			raw, err = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		if err == nil && status == http.StatusOK {
			out, parseErr := parseGemini(raw)
			if parseErr == nil {
				return out, nil
			}
			err = parseErr
		} else if err == nil {
			err = fmt.Errorf("gemini: status %d", status)
			if status == http.StatusBadRequest || status == http.StatusForbidden || status == http.StatusUnauthorized {
				return nil, err
			}
			if status == http.StatusTooManyRequests {
				// Daily quota used up: give up (the sync catches it later);
				// per-minute limit: wait as long as Google says.
				text := string(raw)
				if strings.Contains(text, "PerDay") {
					return nil, errors.New("gemini: daily quota used up")
				}
				wait := 30 * time.Second
				if i := strings.Index(text, "\"retryDelay\": \""); i >= 0 {
					rest := text[i+len("\"retryDelay\": \""):]
					if j := strings.Index(rest, "\""); j > 0 {
						if d, perr := time.ParseDuration(rest[:j]); perr == nil {
							wait = d + time.Second
						}
					}
				}
				if attempt < 5 {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(wait):
					}
					continue
				}
			}
		}
		if attempt >= 5 || ctx.Err() != nil {
			return nil, err
		}
		wait := time.Duration(1<<attempt) * 2 * time.Second
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func parseGemini(raw []byte) (map[string]actorFields, error) {
	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if len(resp.Candidates) == 0 {
		return nil, errors.New("gemini: no candidates")
	}
	c := resp.Candidates[0]
	if c.FinishReason != "" && c.FinishReason != "STOP" {
		return nil, fmt.Errorf("gemini: finish reason %s", c.FinishReason)
	}
	var text strings.Builder
	for _, part := range c.Content.Parts {
		if !part.Thought {
			text.WriteString(part.Text)
		}
	}
	out := map[string]actorFields{}
	if err := json.Unmarshal([]byte(text.String()), &out); err != nil {
		return nil, fmt.Errorf("gemini: bad JSON: %w", err)
	}
	return out, nil
}
