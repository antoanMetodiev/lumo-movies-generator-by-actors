package generator

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newHandlersWithNilSvc() *Handlers {
	return &Handlers{svc: nil}
}

// --- searchMovies: runs synchronously (no recover!), mirroring the Java
// controller's own behavior of letting an exception propagate. That means
// any scenario reaching a real TMDB call MUST use a working test server
// here -- calling it with a nil svc would crash the test binary, not just
// fail the test. Only the true no-op branches (blank/whitespace title) are
// safe to exercise against a nil svc. ---

func TestSearchMovies_BlankBody_NoOp(t *testing.T) {
	h := newHandlersWithNilSvc()
	r := httptest.NewRequest(http.MethodPost, "/search-movies", strings.NewReader(""))
	rec := httptest.NewRecorder()

	h.searchMovies(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (default, unwritten) for a blank body", rec.Code)
	}
}

func TestSearchMovies_WhitespaceOnlyBody_NoOp(t *testing.T) {
	h := newHandlersWithNilSvc()
	r := httptest.NewRequest(http.MethodPost, "/search-movies", strings.NewReader("   "))
	rec := httptest.NewRecorder()

	h.searchMovies(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a whitespace-only body (SearchForMovies trims before checking blank)", rec.Code)
	}
}

// TestSearchMovies_JSONQuotedTitle_UnwrapsWhenNoSurroundingWhitespace checks
// the happy-path unwrap: a body that is exactly a JSON-quoted string, with
// no extra characters before/after the quotes, gets unwrapped to its bare
// content before being handed to TMDB as the search query.
func TestSearchMovies_JSONQuotedTitle_UnwrapsWhenNoSurroundingWhitespace(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Write([]byte(`{"results": []}`))
	}))
	defer srv.Close()

	h := &Handlers{svc: newTestService(srv.URL)}
	r := httptest.NewRequest(http.MethodPost, "/search-movies", strings.NewReader(`"Inception"`))
	rec := httptest.NewRecorder()

	h.searchMovies(rec, r)

	if gotQuery != "Inception" {
		t.Errorf("TMDB query = %q, want %q (quotes stripped)", gotQuery, "Inception")
	}
}

// TestSearchMovies_LeadingWhitespace_QuoteUnwrapNotApplied documents a real,
// preserved-as-is difference from lumo-movies-generator's own /search-movies
// handler: that one trims the body *before* checking for surrounding
// quotes, this one does not (`title := string(body)`, no TrimSpace). So a
// body like ` "Inception"` (leading space) fails the `title[0] == '"'`
// check and is sent to TMDB completely unwrapped, quotes and all.
func TestSearchMovies_LeadingWhitespace_QuoteUnwrapNotApplied(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Write([]byte(`{"results": []}`))
	}))
	defer srv.Close()

	h := &Handlers{svc: newTestService(srv.URL)}
	raw := ` "Inception"`
	r := httptest.NewRequest(http.MethodPost, "/search-movies", strings.NewReader(raw))
	rec := httptest.NewRecorder()

	h.searchMovies(rec, r)

	if gotQuery != raw {
		t.Errorf("TMDB query = %q, want the raw unwrapped body %q (leading whitespace defeats the quote check)", gotQuery, raw)
	}
}

type erroringBody struct{}

func (erroringBody) Read([]byte) (int, error) { return 0, errors.New("boom") }
func (erroringBody) Close() error             { return nil }

func TestSearchMovies_BodyReadError_Returns400(t *testing.T) {
	h := newHandlersWithNilSvc()
	r := httptest.NewRequest(http.MethodPost, "/search-movies", nil)
	r.Body = erroringBody{}
	rec := httptest.NewRecorder()

	h.searchMovies(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when the request body can't be read", rec.Code)
	}
}

var _ io.ReadCloser = erroringBody{}

// --- addMoviesByActor: fire-and-forget (unlike searchMovies above) -- the
// handler hands off to a goroutine with its own recover(), so the caller
// always gets 200 immediately regardless of what happens in the
// background. AddMoviesByActor's very first line dereferences s.tmdb, so a
// nil svc panics right away, inside that goroutine's own recover -- safe to
// exercise directly, and it proves the panic-recovery contract without ever
// reaching a real network call. ---

func TestAddMoviesByActor_ReturnsImmediately200_EvenThoughBackgroundCallPanics(t *testing.T) {
	h := newHandlersWithNilSvc()
	r := httptest.NewRequest(http.MethodPost, "/add-movies-by-actor/nm0000138", nil)
	r.SetPathValue("imdb_id", "nm0000138")
	rec := httptest.NewRecorder()

	h.addMoviesByActor(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (the handler returns before the background goroutine even runs)", rec.Code)
	}

	// Give the background goroutine a moment to actually panic and recover,
	// so a regression that removes the recover() would crash this test
	// binary instead of silently passing.
	time.Sleep(20 * time.Millisecond)
}
