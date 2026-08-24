// Package generator mirrors MoviesGeneratorByActorsService — a write-only
// pipeline that pulls a movie's full data from TMDB and populates the
// shared "movies" catalog table (the same one lumo-movies-svc owns).
package generator

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lumo-movies-generator-by-actors/internal/models"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// FindByVideoURL mirrors MovieRepository.findByVideoURL — the dedup check
// saveMovie uses. Only the id is actually needed by any caller here.
func (r *Repository) FindByVideoURL(ctx context.Context, videoURL string) (*uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, "SELECT id FROM movies WHERE video_url = $1", videoURL).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &id, nil
}

// decodeStringList / encodeStringList mirror StringListConverter — "{}" or
// NULL <-> empty list, otherwise a "{a,b,c}" string split/joined on commas
// (no escaping, matching the Java converter's own limitation exactly).
// Only encode is actually needed here (this service never reads a movie
// back out), kept for parity with the other lumo-* Go rewrites.
func encodeStringList(list []string) string {
	if len(list) == 0 {
		return "{}"
	}
	return "{" + strings.Join(list, ",") + "}"
}

// InsertMovie mirrors MoviesGeneratorByActorsService.saveMovie: one movie
// row (including this service's own video_urls column, always empty —
// never populated anywhere in the original either), its images, and the
// movies_actors links to already-managed actor ids. Runs in a transaction
// since it's several statements that must land together. tmdb_id,
// vidm_player, fmoon_player, abs_player, vidme_player, and players_checked
// are left at their column defaults (NULL/false) — this service's own Movie
// entity never had those fields to begin with.
func (r *Repository) InsertMovie(ctx context.Context, m *models.Movie, castActorIDs []uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO movies (id, version, title, poster_img_url, special_text, trailer_url,
			search_tag, genres, description, release_date, tmdb_rating, background_img_url, created_at,
			video_url, trailer_urls, video_urls)
		VALUES ($1,0,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		m.ID, m.Title, m.PosterImgURL, m.SpecialText, m.TrailerURL,
		m.SearchTag, m.Genres, m.Description, m.ReleaseDate, m.TmdbRating, m.BackgroundImgURL, m.CreatedAt,
		m.VideoURL, encodeStringList(m.TrailerURLs), encodeStringList(m.VideoURLs),
	)
	if err != nil {
		return err
	}

	for _, img := range m.ImagesList {
		if img.ID == uuid.Nil {
			img.ID = uuid.New()
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO movies_images (id, version, image_type, image_url, movie_id) VALUES ($1,0,$2,$3,$4)`,
			img.ID, img.ImageType, img.ImageURL, m.ID)
		if err != nil {
			return err
		}
	}

	for _, actorID := range castActorIDs {
		_, err = tx.Exec(ctx,
			`INSERT INTO movies_actors (movie_id, actor_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			m.ID, actorID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
