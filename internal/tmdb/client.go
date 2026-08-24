// Package tmdb is a thin TMDB API client mirroring how the Java service used
// Gson's dynamic JsonObject/JsonArray — plain map[string]any instead of
// fixed response structs, since the original code only ever plucks a
// handful of fields out of much larger TMDB payloads. Call sites build the
// full request URL themselves (same as the Java code did with plain string
// concatenation), so the two stay easy to compare line-for-line.
package tmdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	HTTP    *http.Client
	BaseURL string // TMDB_BASE_URL
	APIKey  string // TMDB_API_KEY
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		BaseURL: baseURL,
		APIKey:  apiKey,
	}
}

// GetURL issues a GET against a fully-formed URL and decodes the JSON body
// into a plain map. Numbers decode as json.Number (their exact original
// text) rather than float64 — the same effect as Gson's
// JsonPrimitive.getAsString() on a JSON number, with no float round-trip.
// Returns a nil map (not an error) for any non-200 response, mirroring every
// call site's `if (response.statusCode() == 200)` guard.
func GetURL(client *http.Client, fullURL string) (map[string]any, int, error) {
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, 0, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}

	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()

	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, resp.StatusCode, err
	}

	return out, resp.StatusCode, nil
}

func (c *Client) Get(fullURL string) (map[string]any, int, error) {
	return GetURL(c.HTTP, fullURL)
}

// URL builds TMDB_BASE_URL + path + "?api_key=..." (+ extra, verbatim, e.g.
// "&language=en-US") — the exact pattern every Java call site used.
func (c *Client) URL(path string, extra string) string {
	return c.BaseURL + path + "?api_key=" + url.QueryEscape(c.APIKey) + extra
}

// Str mirrors UtilMethods.getJsonValue: "" for a missing/null key, otherwise
// the value stringified (numbers via their exact original text, thanks to
// json.Number — same effect as Gson's JsonPrimitive.getAsString()).
func Str(obj map[string]any, key string) string {
	if obj == nil {
		return ""
	}
	v, ok := obj[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		return fmt.Sprintf("%v", t)
	}
}

// Arr reads a key as a []any, or nil if missing/wrong type — mirrors
// JsonObject.getAsJsonArray() returning null.
func Arr(obj map[string]any, key string) []any {
	v, ok := obj[key]
	if !ok || v == nil {
		return nil
	}
	arr, _ := v.([]any)
	return arr
}

// Obj reads a key as a map[string]any, or nil.
func Obj(obj map[string]any, key string) map[string]any {
	v, ok := obj[key]
	if !ok || v == nil {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

// AsObj type-asserts a []any element (from Arr) into a map[string]any.
func AsObj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
