package generator

import (
	"context"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"lumo-movies-generator-by-actors/internal/actors"
	"lumo-movies-generator-by-actors/internal/models"
	"lumo-movies-generator-by-actors/internal/tmdb"
)

// Service mirrors MoviesGeneratorByActorsService end to end.
type Service struct {
	tmdb       *tmdb.Client
	repo       *Repository
	actorsRepo *actors.Repository
}

func NewService(tmdbClient *tmdb.Client, repo *Repository, actorsRepo *actors.Repository) *Service {
	return &Service{tmdb: tmdbClient, repo: repo, actorsRepo: actorsRepo}
}

// SearchForMovies mirrors MoviesGeneratorByActorsService.searchForMovies.
func (s *Service) SearchForMovies(ctx context.Context, movieName string) {
	if strings.TrimSpace(movieName) == "" {
		log.Printf("searchForMovies: blank title, ignoring")
		return
	}
	log.Printf("searchForMovies: ordered %q — querying TMDB", movieName)

	searchURL := s.tmdb.BaseURL + "/3/search/movie?api_key=" + url.QueryEscape(s.tmdb.APIKey) +
		"&query=" + url.QueryEscape(movieName)

	body, status, err := s.tmdb.Get(searchURL)
	if err != nil {
		log.Printf("searchForMovies(%q): TMDB request failed: %v", movieName, err)
		return
	}
	log.Printf("searchForMovies(%q): TMDB responded status=%d", movieName, status)
	s.processMovies(ctx, body, status)
}

// AddMoviesByActor mirrors MoviesGeneratorByActorsService.addMoviesByActor:
// resolve a TMDB person id from an imdb id, then process every one of that
// person's movie credits — concurrently, but capped at 2 in flight at once
// via a semaphore, exactly like the Java code's `new Semaphore(2)` +
// virtual-thread executor. Each movie is fire-and-forget (the Java method
// itself doesn't wait for these either — it returns as soon as every
// executor.execute() call has been submitted).
func (s *Service) AddMoviesByActor(ctx context.Context, imdbID string) {
	log.Printf("addMoviesByActor(%q): resolving TMDB person id", imdbID)
	findURL := "https://api.themoviedb.org/3/find/" + url.QueryEscape(imdbID) +
		"?external_source=imdb_id&api_key=" + url.QueryEscape(s.tmdb.APIKey)

	findBody, status, err := s.tmdb.Get(findURL)
	if err != nil || status != 200 || findBody == nil {
		log.Printf("addMoviesByActor(%q): /find failed (status=%d): %v", imdbID, status, err)
		return
	}

	personResults := tmdb.Arr(findBody, "person_results")
	if len(personResults) == 0 {
		log.Printf("addMoviesByActor(%q): no TMDB person found", imdbID)
		return
	}

	firstPerson := tmdb.AsObj(personResults[0])
	personID := tmdb.Str(firstPerson, "id")
	personName := tmdb.Str(firstPerson, "name")
	log.Printf("addMoviesByActor(%q): resolved %q (tmdbPersonId=%s), fetching movie credits", imdbID, personName, personID)

	creditsURL := "https://api.themoviedb.org/3/person/" + personID + "/movie_credits?api_key=" + url.QueryEscape(s.tmdb.APIKey)
	creditsBody, status, err := s.tmdb.Get(creditsURL)
	if err != nil || status != 200 || creditsBody == nil {
		log.Printf("addMoviesByActor(%q): no movie credits found (status=%d): %v", imdbID, status, err)
		return
	}

	castMovies := tmdb.Arr(creditsBody, "cast")
	if castMovies == nil {
		log.Printf("addMoviesByActor(%q): no cast movies in credits response", imdbID)
		return
	}
	log.Printf("addMoviesByActor(%q): %q has %d movie credit(s), processing each (max 2 concurrent)", imdbID, personName, len(castMovies))

	sem := make(chan struct{}, 2)
	for _, elem := range castMovies {
		movieJSON := tmdb.AsObj(elem)
		if movieJSON == nil {
			continue
		}
		movieID := tmdb.Str(movieJSON, "id")
		title := tmdb.Str(movieJSON, "title")
		if title == "" {
			title = tmdb.Str(movieJSON, "original_title")
		}
		if title == "" {
			title = "N/A"
		}
		character := tmdb.Str(movieJSON, "character")
		log.Printf("Movie: %s (%s) as %s", title, movieID, character)

		sem <- struct{}{}
		go func(movieID, title string) {
			defer func() { <-sem }()
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("addMoviesByActor: processing movie %q (tmdbId=%s) panic: %v", title, movieID, rec)
				}
			}()

			movieURL := s.tmdb.URL("/3/movie/"+movieID, "")
			body, status, err := s.tmdb.Get(movieURL)
			if err != nil {
				log.Printf("addMoviesByActor: fetching movie %q (tmdbId=%s): %v", title, movieID, err)
				return
			}
			s.processMovies(ctx, body, status)
		}(movieID, title)
	}
}

// processMovies mirrors MoviesGeneratorByActorsService.processMovies:
// `body` may be a TMDB search response ({"results": [...]}) or a single
// bare movie object (from the by-id lookup in AddMoviesByActor) — both are
// normalized to a slice of movie objects the same way the Java code did.
func (s *Service) processMovies(ctx context.Context, body map[string]any, status int) {
	if status != 200 || body == nil {
		log.Printf("processMovies: nothing to process (status=%d, empty body=%v)", status, body == nil)
		return
	}

	var results []any
	if r := tmdb.Arr(body, "results"); r != nil {
		results = r
	} else {
		results = []any{body}
	}
	log.Printf("processMovies: %d TMDB result(s) returned", len(results))

	for _, elem := range results {
		movieJSON := tmdb.AsObj(elem)
		if movieJSON == nil {
			continue
		}
		s.processOneMovie(ctx, movieJSON)
	}
}

func (s *Service) processOneMovie(ctx context.Context, movieJSON map[string]any) {
	movieID := tmdb.Str(movieJSON, "id")
	title := tmdb.Str(movieJSON, "title")
	description := tmdb.Str(movieJSON, "overview")
	releaseDate := tmdb.Str(movieJSON, "release_date")
	backgroundIMG := tmdb.Str(movieJSON, "backdrop_path")
	posterIMG := tmdb.Str(movieJSON, "poster_path")
	movieRating := tmdb.Str(movieJSON, "vote_average")

	if strings.TrimSpace(posterIMG) == "" {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): no poster image", title, movieID)
		return
	}
	if strings.TrimSpace(releaseDate) == "" {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): no release date", title, movieID)
		return
	}
	parsedDate, err := time.Parse("2006-01-02", releaseDate)
	if err != nil {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): unparseable release date %q", title, movieID, releaseDate)
		return
	}
	if parsedDate.After(time.Now()) {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): not released yet (%s)", title, movieID, releaseDate)
		return
	}
	if parsedDate.Year() < 2000 {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): released before 2000 (%s)", title, movieID, releaseDate)
		return
	}
	if movieRating == "0.0" {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): TMDB rating is 0.0", title, movieID)
		return
	}
	log.Printf("processOneMovie: %q (tmdbId=%s) passed initial checks, fetching cast/images/genres", title, movieID)

	trailerURL := s.getTrailerFromAPI(movieID)

	videoURL := "https://vidsrc.icu/embed/movie/" + movieID
	castURL := s.tmdb.URL("/3/movie/"+movieID+"/credits", "")

	var wg sync.WaitGroup
	var castActors []models.Actor
	var images []models.MovieImage
	var imagesOK bool
	var genres, tagline string
	var genresOK bool

	wg.Add(3)
	go func() { defer wg.Done(); castActors = s.extractActors(castURL) }()
	go func() { defer wg.Done(); images, imagesOK = s.extractImages(movieID) }()
	go func() { defer wg.Done(); genres, tagline, genresOK = s.extractGenresAndTagline(movieID) }()
	wg.Wait()

	movie := &models.Movie{}
	s.addAllCast(ctx, castActors, movie)
	if len(castActors) == 0 {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): no resolvable cast", title, movieID)
		return
	}
	if !imagesOK {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): fewer than 4 images", title, movieID)
		return
	}
	if !genresOK {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): no genres", title, movieID)
		return
	}

	movie.VideoURL = videoURL
	movie.SearchTag = title
	movie.Title = title
	if description != "" {
		movie.Description = &description
	}
	movie.ReleaseDate = &releaseDate
	movie.BackgroundImgURL = &backgroundIMG
	movie.PosterImgURL = posterIMG
	movie.TmdbRating = &movieRating
	if trailerURL != "" {
		movie.TrailerURL = &trailerURL
	}
	movie.CreatedAt = time.Now().UTC()
	movie.ImagesList = images
	if genres != "" {
		movie.Genres = genres
	}
	if tagline != "" {
		movie.SpecialText = &tagline
	}

	log.Printf("processOneMovie: %q (tmdbId=%s) ready to save (cast=%d, images=%d, genres=%q)", title, movieID, len(movie.CastList), len(movie.ImagesList), genres)
	s.saveMovie(ctx, videoURL, movie)
}

func (s *Service) getTrailerFromAPI(movieID string) string {
	body, status, err := s.tmdb.Get(s.tmdb.URL("/3/movie/"+movieID+"/videos", ""))
	if err != nil || status != 200 || body == nil {
		return ""
	}

	for _, v := range tmdb.Arr(body, "results") {
		video := tmdb.AsObj(v)
		if strings.EqualFold(tmdb.Str(video, "type"), "Trailer") && strings.EqualFold(tmdb.Str(video, "site"), "YouTube") {
			return "https://www.youtube.com/watch?v=" + tmdb.Str(video, "key")
		}
	}
	return ""
}

// extractImages mirrors extractImagesAsync. ok=false means "found images
// but fewer than 4" (skip this movie); ok=true with a non-200/error
// response means "couldn't check, don't skip" — same asymmetry as the Java
// code.
func (s *Service) extractImages(movieID string) ([]models.MovieImage, bool) {
	body, status, err := s.tmdb.Get(s.tmdb.URL("/3/movie/"+movieID+"/images", ""))
	if err != nil || status != 200 || body == nil {
		return nil, true
	}

	backdrops := s.extractDetailsImages(tmdb.Arr(body, "backdrops"), models.ImageTypeBackdrop, 29)
	posters := s.extractDetailsImages(tmdb.Arr(body, "posters"), models.ImageTypePoster, 8)
	all := append(backdrops, posters...)

	if len(all) < 4 {
		return nil, false
	}
	return all, true
}

func (s *Service) extractDetailsImages(arr []any, imageType models.ImageType, limit int) []models.MovieImage {
	if arr == nil {
		return []models.MovieImage{}
	}

	var out []models.MovieImage
	for i, elem := range arr {
		obj := tmdb.AsObj(elem)
		if obj == nil {
			continue
		}
		filePath, _ := obj["file_path"].(string)
		out = append(out, models.MovieImage{ImageType: imageType, ImageURL: filePath})
		if i == limit {
			break
		}
	}
	return out
}

// extractGenresAndTagline mirrors extractGenresAndTaglineAsync — genres
// joined with a trailing comma, no spaces, exactly like the Java
// StringBuilder loop (append(name).append(",")).
func (s *Service) extractGenresAndTagline(movieID string) (genres, tagline string, ok bool) {
	body, status, err := s.tmdb.Get(s.tmdb.URL("/3/movie/"+movieID, ""))
	if err != nil || status != 200 || body == nil {
		return "", "", true
	}

	tagline = tmdb.Str(body, "tagline")
	var sb strings.Builder
	for _, genreElem := range tmdb.Arr(body, "genres") {
		obj := tmdb.AsObj(genreElem)
		sb.WriteString(tmdb.Str(obj, "name"))
		sb.WriteString(",")
	}
	genres = sb.String()

	if genres == "" {
		return "", "", false
	}
	return genres, tagline, true
}

// extractActors mirrors UtilMethods.extractActors + extractDeepActorInfoAsync.
func (s *Service) extractActors(castURL string) []models.Actor {
	body, status, err := s.tmdb.Get(castURL)
	if err != nil || status != 200 || body == nil {
		return nil
	}

	castArr := tmdb.Arr(body, "cast")
	results := make([]*models.Actor, len(castArr))

	var wg sync.WaitGroup
	for i, elem := range castArr {
		obj := tmdb.AsObj(elem)
		actorID := tmdb.Str(obj, "id")
		if actorID == "" {
			continue
		}
		wg.Add(1)
		go func(i int, actorID string) {
			defer wg.Done()
			results[i] = s.extractDeepActorInfo(actorID)
		}(i, actorID)
	}
	wg.Wait()

	var out []models.Actor
	for _, a := range results {
		if a != nil {
			out = append(out, *a)
		}
	}
	return out
}

// extractDeepActorInfo mirrors UtilMethods.extractDeepActorInfoAsync —
// skips an actor entirely when TMDB has no imdb_id for them (this service
// resolves duplicates by imdb_id, same as lumo-movies-svc).
func (s *Service) extractDeepActorInfo(actorID string) *models.Actor {
	searchURL := s.tmdb.URL("/3/person/"+actorID, "&append_to_response=external_ids")
	body, status, err := s.tmdb.Get(searchURL)
	if err != nil || status != 200 || body == nil {
		return nil
	}

	name := tmdb.Str(body, "name")
	actorImage := tmdb.Str(body, "profile_path")
	biography := tmdb.Str(body, "biography")
	birthday := tmdb.Str(body, "birthday")
	knownFor := tmdb.Str(body, "known_for_department")
	popularity := tmdb.Str(body, "popularity")
	placeOfBirth := tmdb.Str(body, "place_of_birth")
	gender := tmdb.Str(body, "gender")

	externalIDs := tmdb.Obj(body, "external_ids")
	facebook := tmdb.Str(externalIDs, "facebook_id")
	instagram := tmdb.Str(externalIDs, "instagram_id")
	twitter := tmdb.Str(externalIDs, "twitter_id")
	youtube := tmdb.Str(externalIDs, "youtube_id")
	imdbID := tmdb.Str(externalIDs, "imdb_id")

	if strings.TrimSpace(imdbID) == "" {
		return nil
	}

	return &models.Actor{
		NameInRealLife:    name,
		ImageURL:          strOrNil(actorImage),
		Biography:         strOrNil(biography),
		FacebookUsername:  strOrNil(facebook),
		InstagramUsername: strOrNil(instagram),
		TwitterUsername:   strOrNil(twitter),
		YoutubeChannel:    strOrNil(youtube),
		ImdbID:            strOrNil(imdbID),
		Birthday:          strOrNil(birthday),
		KnownFor:          strOrNil(knownFor),
		Popularity:        strOrNil(popularity),
		PlaceOfBirth:      strOrNil(placeOfBirth),
		Gender:            strOrNil(gender),
	}
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// addAllCast mirrors addAllCast, resolving each TMDB actor against an
// existing DB row by imdb_id first. Capped at 21 cast members — the Java
// `count++ == 20` check runs *after* the 21st actor is already added
// (post-increment compares the pre-increment value), so this reproduces
// that exact off-by-one rather than a clean 20.
func (s *Service) addAllCast(ctx context.Context, allCast []models.Actor, movie *models.Movie) {
	for i, actor := range allCast {
		resolved := actor
		if actor.ImdbID != nil {
			existing, err := s.actorsRepo.FindByIMDBID(ctx, *actor.ImdbID)
			if err == nil && existing != nil {
				resolved = *existing
			}
		}

		movie.CastList = append(movie.CastList, resolved)

		if i == 20 {
			break
		}
	}
}

// saveMovie mirrors saveMovie: skip entirely if a movie with this video_url
// already exists; otherwise persist any not-yet-managed cast members first
// (so they get a real id), then the movie itself with its images and the
// resolved cast id links.
func (s *Service) saveMovie(ctx context.Context, videoURL string, movie *models.Movie) {
	existing, err := s.repo.FindByVideoURL(ctx, videoURL)
	if err != nil {
		log.Printf("saveMovie: findByVideoURL for %q: %v", movie.Title, err)
		return
	}
	if existing != nil {
		log.Printf("saveMovie: %q already in catalog, skipping", movie.Title)
		return
	}

	resolvedIDs := make([]uuid.UUID, 0, len(movie.CastList))
	newActors := 0
	for i := range movie.CastList {
		actor := &movie.CastList[i]
		if actor.ID != uuid.Nil {
			resolvedIDs = append(resolvedIDs, actor.ID)
			continue
		}
		if err := s.actorsRepo.Save(ctx, actor); err != nil {
			log.Printf("saveMovie: persisting new actor %q for %q: %v", actor.NameInRealLife, movie.Title, err)
			continue
		}
		newActors++
		resolvedIDs = append(resolvedIDs, actor.ID)
	}
	log.Printf("saveMovie: %q — %d cast member(s) resolved (%d newly created), inserting movie", movie.Title, len(resolvedIDs), newActors)

	if err := s.repo.InsertMovie(ctx, movie, resolvedIDs); err != nil {
		log.Printf("saveMovie: insert %q FAILED: %v", movie.Title, err)
		return
	}
	log.Printf("saveMovie: %q added to the catalog", movie.Title)
}
