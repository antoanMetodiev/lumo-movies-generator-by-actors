// Package actors mirrors commonData.repositories.ActorRepository — same SQL,
// same shared "actors" table lumo-movies-svc/lumo-series-svc also read/write.
package actors

import (
	"context"

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

func scanActor(row pgx.CollectableRow) (models.Actor, error) {
	var a models.Actor
	err := row.Scan(
		&a.ID, &a.ImageURL, &a.Biography, &a.FacebookUsername, &a.InstagramUsername,
		&a.TwitterUsername, &a.YoutubeChannel, &a.ImdbID, &a.Birthday, &a.KnownFor,
		&a.PlaceOfBirth, &a.Gender, &a.Popularity, &a.NameInRealLife,
	)
	return a, err
}

const actorColumns = "id, image_url, biography, facebook_username, instagram_username, " +
	"twitter_username, youtube_channel, imdb_id, birthday, known_for, place_of_birth, " +
	"gender, popularity, name_in_real_life"

// FindByIMDBID mirrors ActorRepository.findByIMDB_ID.
func (r *Repository) FindByIMDBID(ctx context.Context, imdbID string) (*models.Actor, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+actorColumns+" FROM actors WHERE imdb_id = $1", imdbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	a, err := pgx.CollectExactlyOneRow(rows, scanActor)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// FindByID mirrors JpaRepository.findById(UUID) as used by saveMovie.
func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*models.Actor, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+actorColumns+" FROM actors WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	a, err := pgx.CollectExactlyOneRow(rows, scanActor)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// Save inserts a new actor if a.ID is the zero UUID, otherwise upserts by id
// — mirrors JpaRepository.save() (Hibernate does an INSERT for a
// transient/new entity, UPDATE for a managed one).
func (r *Repository) Save(ctx context.Context, a *models.Actor) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
		_, err := r.pool.Exec(ctx,
			`INSERT INTO actors (id, image_url, biography, facebook_username, instagram_username,
				twitter_username, youtube_channel, imdb_id, birthday, known_for, place_of_birth,
				gender, popularity, name_in_real_life)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			a.ID, a.ImageURL, a.Biography, a.FacebookUsername, a.InstagramUsername,
			a.TwitterUsername, a.YoutubeChannel, a.ImdbID, a.Birthday, a.KnownFor,
			a.PlaceOfBirth, a.Gender, a.Popularity, a.NameInRealLife,
		)
		return err
	}

	_, err := r.pool.Exec(ctx,
		`UPDATE actors SET image_url=$2, biography=$3, facebook_username=$4, instagram_username=$5,
			twitter_username=$6, youtube_channel=$7, imdb_id=$8, birthday=$9, known_for=$10,
			place_of_birth=$11, gender=$12, popularity=$13, name_in_real_life=$14
		WHERE id=$1`,
		a.ID, a.ImageURL, a.Biography, a.FacebookUsername, a.InstagramUsername,
		a.TwitterUsername, a.YoutubeChannel, a.ImdbID, a.Birthday, a.KnownFor,
		a.PlaceOfBirth, a.Gender, a.Popularity, a.NameInRealLife,
	)
	return err
}
