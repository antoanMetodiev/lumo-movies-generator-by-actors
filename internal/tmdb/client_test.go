package tmdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Str ---

func TestStr_MissingKeyReturnsEmpty(t *testing.T) {
	obj := map[string]any{"title": "Inception"}
	if got := Str(obj, "does_not_exist"); got != "" {
		t.Errorf("Str() = %q, want empty string for missing key", got)
	}
}

func TestStr_NilValueReturnsEmpty(t *testing.T) {
	obj := map[string]any{"tagline": nil}
	if got := Str(obj, "tagline"); got != "" {
		t.Errorf("Str() = %q, want empty string for a JSON null value", got)
	}
}

func TestStr_NilObjectReturnsEmpty(t *testing.T) {
	if got := Str(nil, "title"); got != "" {
		t.Errorf("Str(nil, ...) = %q, want empty string", got)
	}
}

func TestStr_StringValue(t *testing.T) {
	obj := map[string]any{"title": "Inception"}
	if got := Str(obj, "title"); got != "Inception" {
		t.Errorf("Str() = %q, want %q", got, "Inception")
	}
}

// TestStr_NumberValue mirrors Gson's JsonPrimitive.getAsString() on a JSON
// number — the exact original text, not a float64 round-trip (which would
// mangle something like a large TMDB id or "7.80" into "7.8").
func TestStr_NumberValue(t *testing.T) {
	cases := []struct {
		name string
		json string
		key  string
		want string
	}{
		{"integer id", `{"id": 1064213}`, "id", "1064213"},
		{"decimal rating", `{"vote_average": 7.8}`, "vote_average", "7.8"},
		{"zero rating", `{"vote_average": 0.0}`, "vote_average", "0.0"},
		{"large id preserves precision", `{"id": 9007199254740993}`, "id", "9007199254740993"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var obj map[string]any
			dec := json.NewDecoder(strings.NewReader(tc.json))
			dec.UseNumber()
			if err := dec.Decode(&obj); err != nil {
				t.Fatalf("decode: %v", err)
			}

			if got := Str(obj, tc.key); got != tc.want {
				t.Errorf("Str() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStr_BoolValue(t *testing.T) {
	obj := map[string]any{"adult": false}
	if got := Str(obj, "adult"); got != "false" {
		t.Errorf("Str() = %q, want %q", got, "false")
	}
}

// --- Arr ---

func TestArr_MissingKeyReturnsNil(t *testing.T) {
	obj := map[string]any{}
	if got := Arr(obj, "results"); got != nil {
		t.Errorf("Arr() = %v, want nil for missing key", got)
	}
}

func TestArr_NullValueReturnsNil(t *testing.T) {
	obj := map[string]any{"results": nil}
	if got := Arr(obj, "results"); got != nil {
		t.Errorf("Arr() = %v, want nil for a JSON null value", got)
	}
}

func TestArr_WrongTypeReturnsNil(t *testing.T) {
	obj := map[string]any{"results": "not an array"}
	if got := Arr(obj, "results"); got != nil {
		t.Errorf("Arr() = %v, want nil when the value isn't a JSON array", got)
	}
}

func TestArr_ValidArray(t *testing.T) {
	obj := map[string]any{"results": []any{"a", "b", "c"}}
	got := Arr(obj, "results")
	if len(got) != 3 {
		t.Fatalf("Arr() len = %d, want 3", len(got))
	}
}

// --- Obj ---

func TestObj_MissingKeyReturnsNil(t *testing.T) {
	obj := map[string]any{}
	if got := Obj(obj, "external_ids"); got != nil {
		t.Errorf("Obj() = %v, want nil for missing key", got)
	}
}

func TestObj_ValidObject(t *testing.T) {
	obj := map[string]any{"external_ids": map[string]any{"imdb_id": "tt1375666"}}
	got := Obj(obj, "external_ids")
	if got == nil {
		t.Fatal("Obj() = nil, want a map")
	}
	if got["imdb_id"] != "tt1375666" {
		t.Errorf("Obj()[imdb_id] = %v, want tt1375666", got["imdb_id"])
	}
}

// --- AsObj ---

func TestAsObj_ValidObject(t *testing.T) {
	var v any = map[string]any{"id": "42"}
	got := AsObj(v)
	if got == nil || got["id"] != "42" {
		t.Errorf("AsObj() = %v, want map with id=42", got)
	}
}

func TestAsObj_WrongTypeReturnsNil(t *testing.T) {
	var v any = "not an object"
	if got := AsObj(v); got != nil {
		t.Errorf("AsObj() = %v, want nil for a non-object value", got)
	}
}

// --- URL ---

func TestClient_URL_BuildsExpectedFormat(t *testing.T) {
	c := New("https://api.themoviedb.org", "my api key")
	got := c.URL("/3/movie/550", "&language=en-US")
	want := "https://api.themoviedb.org/3/movie/550?api_key=my+api+key&language=en-US"
	if got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}

func TestClient_URL_NoExtra(t *testing.T) {
	c := New("https://api.themoviedb.org", "key123")
	got := c.URL("/3/movie/550/credits", "")
	want := "https://api.themoviedb.org/3/movie/550/credits?api_key=key123"
	if got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}

// --- GetURL / Get (mocked HTTP) ---

func TestGetURL_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": 550, "title": "Fight Club"}`))
	}))
	defer srv.Close()

	body, status, err := GetURL(srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("GetURL() error = %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if Str(body, "title") != "Fight Club" {
		t.Errorf("body[title] = %q, want %q", Str(body, "title"), "Fight Club")
	}
	if Str(body, "id") != "550" {
		t.Errorf("body[id] = %q, want %q (exact original numeric text)", Str(body, "id"), "550")
	}
}

// TestGetURL_NonOKStatus mirrors every Java call site's own
// `if (response.statusCode() == 200)` guard: a non-200 response is not an
// error, it's a nil body the caller is expected to check the status for.
func TestGetURL_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"status_message": "not found"}`))
	}))
	defer srv.Close()

	body, status, err := GetURL(srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("GetURL() error = %v, want nil error even on a non-200 response", err)
	}
	if status != 404 {
		t.Errorf("status = %d, want 404", status)
	}
	if body != nil {
		t.Errorf("body = %v, want nil on a non-200 response", body)
	}
}

func TestGetURL_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()

	_, status, err := GetURL(srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("GetURL() error = nil, want a decode error for malformed JSON")
	}
	if status != 200 {
		t.Errorf("status = %d, want 200 (the HTTP layer succeeded, only decoding failed)", status)
	}
}

func TestGetURL_NetworkError(t *testing.T) {
	// Nothing is listening on this port.
	_, _, err := GetURL(http.DefaultClient, "http://127.0.0.1:1/unreachable")
	if err == nil {
		t.Fatal("GetURL() error = nil, want a connection error")
	}
}

func TestClient_Get_UsesConfiguredHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok": true}`))
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, APIKey: "unused"}
	body, status, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if status != 200 || Str(body, "ok") != "true" {
		t.Errorf("Get() = %v, %d, want ok=true, 200", body, status)
	}
}
