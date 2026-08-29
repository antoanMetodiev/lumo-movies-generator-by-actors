// Package models is intentionally minimal: neither of this service's two
// endpoints returns a JSON body (both are fire-and-forget writes), so these
// structs only ever need to round-trip through the database, never through
// Jackson-compatible JSON.
package models

import (
	"time"

	"github.com/google/uuid"
)

type ImageType string

const (
	ImageTypeBackdrop ImageType = "BACKDROP"
	ImageTypePoster   ImageType = "POSTER"
)

// Actor mirrors commonData.models.entities.Actor — the same shared "actors"
// table lumo-movies-svc/lumo-series-svc also read/write.
type Actor struct {
	ID                uuid.UUID
	ImageURL          *string
	Biography         *string
	FacebookUsername  *string
	InstagramUsername *string
	TwitterUsername   *string
	YoutubeChannel    *string
	ImdbID            *string
	Birthday          *string
	KnownFor          *string
	PlaceOfBirth      *string
	Gender            *string
	Popularity        *string
	NameInRealLife    string
}

// MovieImage mirrors models.entities.MovieImage.
type MovieImage struct {
	ID        uuid.UUID
	ImageType ImageType
	ImageURL  string
}

// Movie mirrors models.entities.Movie + its CinemaRecord base fields. Note
// this service's own Movie class has no vidmPlayer/fmoonPlayer/absPlayer/
// vidmePlayer/playersChecked columns (unlike lumo-movies-svc's version of
// the same shared "movies" table) — those are simply left NULL on insert
// here, same as the Java entity leaving them unset. It does have an extra
// video_urls column, always empty here (never populated anywhere in the
// original service, despite the field existing).
//
// TmdbID (added post-rewrite, per the user): the original Java entity here
// never had it either, leaving every row this service inserted with a NULL
// tmdb_id — the frontend then had to fall back to parsing the trailing
// number off video_url, which breaks for any row missing that URL. This
// service already extracts the real TMDB id (processOneMovie's movieID) for
// every movie it saves, so it's captured here too instead of discarded.
type Movie struct {
	ID uuid.UUID

	Title            string
	PosterImgURL     string
	SpecialText      *string
	TrailerURL       *string
	SearchTag        string
	Genres           string
	Description      *string
	ReleaseDate      *string
	TmdbRating       *string
	BackgroundImgURL *string
	CreatedAt        time.Time

	CastList    []Actor
	VideoURL    string
	TmdbID      *string
	ImagesList  []MovieImage
	TrailerURLs []string
	VideoURLs   []string
}
