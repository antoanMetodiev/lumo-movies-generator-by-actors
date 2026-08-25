package generator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"lumo-movies-generator-by-actors/internal/models"
	"lumo-movies-generator-by-actors/internal/tmdb"
)

// newTestService builds a Service pointed at a test TMDB server, with no
// repo/actorsRepo — safe for any method that doesn't touch the database.
func newTestService(baseURL string) *Service {
	return &Service{tmdb: tmdb.New(baseURL, "test-api-key")}
}

// --- strOrNil ---

func TestStrOrNil(t *testing.T) {
	if got := strOrNil(""); got != nil {
		t.Errorf("strOrNil(\"\") = %v, want nil", got)
	}
	if got := strOrNil("hello"); got == nil || *got != "hello" {
		t.Errorf("strOrNil(\"hello\") = %v, want pointer to \"hello\"", got)
	}
}

// --- extractDetailsImages ---

func TestExtractDetailsImages_NilArray(t *testing.T) {
	s := &Service{}
	got := s.extractDetailsImages(nil, models.ImageTypeBackdrop, 10)
	if len(got) != 0 {
		t.Errorf("extractDetailsImages(nil, ...) = %v, want empty slice", got)
	}
}

func TestExtractDetailsImages_RespectsLimit(t *testing.T) {
	s := &Service{}
	// The Java `if (count++ == limit) break` is a post-increment check, so a
	// limit of 2 actually keeps 3 images (indices 0, 1, 2), not 2 -- e.g. the
	// real backdrops/posters limits of 29/8 actually cap at 30/9.
	arr := []any{
		map[string]any{"file_path": "/a.jpg"},
		map[string]any{"file_path": "/b.jpg"},
		map[string]any{"file_path": "/c.jpg"},
		map[string]any{"file_path": "/d.jpg"},
	}
	got := s.extractDetailsImages(arr, models.ImageTypePoster, 2)
	if len(got) != 3 {
		t.Fatalf("extractDetailsImages() len = %d, want 3 (off-by-one limit semantics)", len(got))
	}
	for _, img := range got {
		if img.ImageType != models.ImageTypePoster {
			t.Errorf("image type = %q, want POSTER", img.ImageType)
		}
	}
	if got[0].ImageURL != "/a.jpg" || got[2].ImageURL != "/c.jpg" {
		t.Errorf("unexpected image URLs: %+v", got)
	}
}

func TestExtractDetailsImages_SkipsNonObjectElements(t *testing.T) {
	s := &Service{}
	arr := []any{"not an object", map[string]any{"file_path": "/valid.jpg"}}
	got := s.extractDetailsImages(arr, models.ImageTypeBackdrop, 10)
	if len(got) != 1 || got[0].ImageURL != "/valid.jpg" {
		t.Errorf("extractDetailsImages() = %+v, want just the one valid entry", got)
	}
}

// --- getTrailerFromAPI ---

func TestGetTrailerFromAPI_FindsYouTubeTrailer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results": [
			{"type": "Teaser", "site": "YouTube", "key": "teaser123"},
			{"type": "Trailer", "site": "Vimeo", "key": "vimeo456"},
			{"type": "Trailer", "site": "YouTube", "key": "abc123"}
		]}`))
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	got := s.getTrailerFromAPI("550")
	want := "https://www.youtube.com/watch?v=abc123"
	if got != want {
		t.Errorf("getTrailerFromAPI() = %q, want %q (skipping non-YouTube/non-Trailer entries)", got, want)
	}
}

func TestGetTrailerFromAPI_NoMatchReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results": [{"type": "Teaser", "site": "YouTube", "key": "x"}]}`))
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	if got := s.getTrailerFromAPI("550"); got != "" {
		t.Errorf("getTrailerFromAPI() = %q, want empty string", got)
	}
}

func TestGetTrailerFromAPI_NonOKStatusReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	if got := s.getTrailerFromAPI("550"); got != "" {
		t.Errorf("getTrailerFromAPI() = %q, want empty string on a non-200 response", got)
	}
}

// --- extractImages ---

func TestExtractImages_EnoughImages_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"backdrops": [{"file_path":"/b1.jpg"},{"file_path":"/b2.jpg"}],
			"posters": [{"file_path":"/p1.jpg"},{"file_path":"/p2.jpg"}]
		}`))
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	images, ok := s.extractImages("550")
	if !ok {
		t.Fatal("extractImages() ok = false, want true (4 images meets the >=4 threshold)")
	}
	if len(images) != 4 {
		t.Errorf("extractImages() len = %d, want 4", len(images))
	}
}

func TestExtractImages_TooFewImages_NotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"backdrops": [{"file_path":"/b1.jpg"}], "posters": []}`))
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	images, ok := s.extractImages("550")
	if ok {
		t.Fatal("extractImages() ok = true, want false (only 1 image, below the 4-image threshold)")
	}
	if images != nil {
		t.Errorf("extractImages() images = %v, want nil", images)
	}
}

// TestExtractImages_NonOKStatus_StillOK documents the Java code's asymmetry:
// a failed/non-200 response is NOT treated as "too few images" — it's
// "couldn't check, don't skip the movie over it".
func TestExtractImages_NonOKStatus_StillOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	images, ok := s.extractImages("550")
	if !ok {
		t.Error("extractImages() ok = false, want true for a non-200 response (not a content failure)")
	}
	if images != nil {
		t.Errorf("extractImages() images = %v, want nil", images)
	}
}

// --- extractGenresAndTagline ---

func TestExtractGenresAndTagline_JoinsWithTrailingComma(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tagline": "Mind is the scene of the crime.", "genres": [
			{"name": "Action"}, {"name": "Sci-Fi"}, {"name": "Thriller"}
		]}`))
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	genres, tagline, ok := s.extractGenresAndTagline("27205")
	if !ok {
		t.Fatal("extractGenresAndTagline() ok = false, want true")
	}
	if genres != "Action,Sci-Fi,Thriller," {
		t.Errorf("genres = %q, want %q (trailing comma, no spaces)", genres, "Action,Sci-Fi,Thriller,")
	}
	if tagline != "Mind is the scene of the crime." {
		t.Errorf("tagline = %q, want the tagline text", tagline)
	}
}

func TestExtractGenresAndTagline_NoGenres_NotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tagline": "", "genres": []}`))
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	genres, _, ok := s.extractGenresAndTagline("1")
	if ok {
		t.Error("extractGenresAndTagline() ok = true, want false when genres is empty")
	}
	if genres != "" {
		t.Errorf("genres = %q, want empty string", genres)
	}
}

func TestExtractGenresAndTagline_NonOKStatus_StillOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	genres, tagline, ok := s.extractGenresAndTagline("1")
	if !ok || genres != "" || tagline != "" {
		t.Errorf("extractGenresAndTagline() = (%q, %q, %v), want (\"\", \"\", true)", genres, tagline, ok)
	}
}

// --- extractDeepActorInfo ---

func TestExtractDeepActorInfo_FullProfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"name": "Leonardo DiCaprio",
			"profile_path": "/leo.jpg",
			"biography": "An actor.",
			"birthday": "1974-11-11",
			"known_for_department": "Acting",
			"popularity": "50.5",
			"place_of_birth": "Los Angeles, CA",
			"gender": "2",
			"external_ids": {
				"facebook_id": "leo.fb",
				"instagram_id": "leo.ig",
				"twitter_id": "leo.tw",
				"youtube_id": "",
				"imdb_id": "nm0000138"
			}
		}`))
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	actor := s.extractDeepActorInfo("6193")
	if actor == nil {
		t.Fatal("extractDeepActorInfo() = nil, want a populated actor")
	}
	if actor.NameInRealLife != "Leonardo DiCaprio" {
		t.Errorf("NameInRealLife = %q", actor.NameInRealLife)
	}
	if actor.ImdbID == nil || *actor.ImdbID != "nm0000138" {
		t.Errorf("ImdbID = %v, want nm0000138", actor.ImdbID)
	}
	if actor.YoutubeChannel != nil {
		t.Errorf("YoutubeChannel = %v, want nil (empty string -> nil)", actor.YoutubeChannel)
	}
}

func TestExtractDeepActorInfo_NoImdbID_ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name": "Nobody", "external_ids": {}}`))
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	if actor := s.extractDeepActorInfo("1"); actor != nil {
		t.Errorf("extractDeepActorInfo() = %+v, want nil for an actor with no imdb_id", actor)
	}
}

func TestExtractDeepActorInfo_NonOKStatus_ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	if actor := s.extractDeepActorInfo("1"); actor != nil {
		t.Errorf("extractDeepActorInfo() = %+v, want nil on a non-200 response", actor)
	}
}

// --- extractActors ---

func TestExtractActors_FiltersOutActorsWithoutImdbID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/3/movie/550/credits", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"cast": [{"id": 1}, {"id": 2}, {"id": 3}]}`))
	})
	mux.HandleFunc("/3/person/1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name": "Has IMDB", "external_ids": {"imdb_id": "nm001"}}`))
	})
	mux.HandleFunc("/3/person/2", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name": "No IMDB", "external_ids": {}}`))
	})
	mux.HandleFunc("/3/person/3", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestService(srv.URL)
	actors := s.extractActors(srv.URL + "/3/movie/550/credits")

	if len(actors) != 1 {
		t.Fatalf("extractActors() len = %d, want 1 (only the actor with an imdb_id survives)", len(actors))
	}
	if actors[0].NameInRealLife != "Has IMDB" {
		t.Errorf("surviving actor = %q, want %q", actors[0].NameInRealLife, "Has IMDB")
	}
}

func TestExtractActors_SkipsCastEntriesWithoutID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/3/movie/550/credits", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"cast": [{"name": "no id field here"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestService(srv.URL)
	actors := s.extractActors(srv.URL + "/3/movie/550/credits")
	if len(actors) != 0 {
		t.Errorf("extractActors() = %+v, want empty (no cast entry had an id)", actors)
	}
}

func TestExtractActors_NonOKStatus_ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newTestService(srv.URL)
	if actors := s.extractActors(srv.URL); actors != nil {
		t.Errorf("extractActors() = %v, want nil on a non-200 response", actors)
	}
}

// --- addAllCast (the off-by-one cap, tested without touching the DB by
// only using actors with a nil ImdbID so the actorsRepo lookup branch,
// which would need a real repository, is never reached) ---

func TestAddAllCast_CapsAtTwentyOne(t *testing.T) {
	s := &Service{}

	allCast := make([]models.Actor, 30)
	for i := range allCast {
		allCast[i] = models.Actor{NameInRealLife: string(rune('A' + i))}
	}

	movie := &models.Movie{}
	s.addAllCast(context.Background(), allCast, movie)

	// Java's `if (count++ == 20) return` runs *after* the 21st actor (index
	// 20) has already been appended, so 21 actors survive, not a clean 20.
	if len(movie.CastList) != 21 {
		t.Fatalf("len(CastList) = %d, want 21 (the documented off-by-one)", len(movie.CastList))
	}
	if movie.CastList[0].NameInRealLife != "A" || movie.CastList[20].NameInRealLife != string(rune('A'+20)) {
		t.Errorf("unexpected cast order: first=%q last=%q", movie.CastList[0].NameInRealLife, movie.CastList[20].NameInRealLife)
	}
}

func TestAddAllCast_FewerThanCapAddsAllOfThem(t *testing.T) {
	s := &Service{}

	allCast := []models.Actor{{NameInRealLife: "A"}, {NameInRealLife: "B"}, {NameInRealLife: "C"}}
	movie := &models.Movie{}
	s.addAllCast(context.Background(), allCast, movie)

	if !reflect.DeepEqual(movie.CastList, allCast) {
		t.Errorf("CastList = %+v, want all 3 actors unchanged", movie.CastList)
	}
}

// --- SearchForMovies: only the blank-title short-circuit is unit-testable
// without a database. ---

func TestSearchForMovies_BlankTitle_NoOp(t *testing.T) {
	// s stays a nil *Service: SearchForMovies must return before
	// dereferencing s.tmdb/s.repo for a blank title, so this must not panic.
	var s *Service
	s.SearchForMovies(context.Background(), "   ")
}
