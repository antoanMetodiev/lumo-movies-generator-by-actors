package generator

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// Handlers mirrors MoviesGeneratorByActorController — same 2 paths, same
// fire-and-forget behavior.
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /add-movies-by-actor/{imdb_id}", h.addMoviesByActor)
	mux.HandleFunc("POST /search-movies", h.searchMovies)
}

func (h *Handlers) addMoviesByActor(w http.ResponseWriter, r *http.Request) {
	imdbID := r.PathValue("imdb_id")
	// Fire-and-forget, matching the Java controller's try/catch-and-log —
	// the caller gets a 200 as soon as the request is accepted. Must not use
	// r.Context() inside the goroutine: net/http cancels the request context
	// as soon as this handler returns, which happens almost immediately here
	// — every downstream DB write in AddMoviesByActor would then fail with
	// "context canceled" after the (real, multi-second) TMDB fetches finish.
	// WithoutCancel keeps any request-scoped values without inheriting that
	// cancellation.
	ctx := context.WithoutCancel(r.Context())
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("addMoviesByActor(%q) panic: %v", imdbID, rec)
			}
		}()
		h.svc.AddMoviesByActor(ctx, imdbID)
	}()
}

func (h *Handlers) searchMovies(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	title := string(body)
	// unquote a JSON string body ("Inception" -> Inception), same effective
	// result as Spring's StringHttpMessageConverter reading a raw request body.
	if len(title) >= 2 && title[0] == '"' && title[len(title)-1] == '"' {
		var unquoted string
		if err := json.Unmarshal([]byte(title), &unquoted); err == nil {
			title = unquoted
		}
	}

	log.Println("====>>> Търся Филми...!!!")
	// The Java controller calls searchForMovies synchronously (not @Async)
	// and lets any checked exception propagate as a 500 — mirrored here by
	// just calling it inline rather than backgrounding it.
	h.svc.SearchForMovies(r.Context(), title)
}
