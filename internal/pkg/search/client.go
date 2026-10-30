// Package search is a small client for Typesense, the search index behind the list endpoints. Postgres stays the source of
// truth: a collection is a derived copy that a service keeps in step on every write (and rebuilds from Postgres on demand),
// and a search returns only ids and a total, so the rows themselves always come from the database.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

// Client talks to one Typesense node. The zero value is not usable; a nil *Client means "no search index configured".
type Client struct {
	base   string
	apiKey string
	http   *http.Client
}

// New returns a client for the node at baseURL; "" returns nil (search not configured).
func New(baseURL, apiKey string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		return nil
	}
	return &Client{
		base:   strings.TrimRight(baseURL, "/"),
		apiKey: apiKey,
		http:   &http.Client{Timeout: 5 * time.Second},
	}
}

// Enabled says whether a search index is configured.
func (c *Client) Enabled() bool { return c != nil }

// ErrNotFound is returned for a collection, alias or document that does not exist.
var ErrNotFound = errors.New("search: not found")

// Unavailable is the error a list endpoint gives when the index cannot be reached: a clear 503, never a silent fallback to
// another source.
func Unavailable(err error) error {
	return apperr.Unavailable("Search is temporarily unavailable. Try again in a moment.").WithCause(err)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string, out any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-TYPESENSE-API-KEY", c.apiKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("typesense %s %s: %d %s", method, path, res.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		if rm, ok := out.(*json.RawMessage); ok {
			*rm = raw
			return nil
		}
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *Client) json(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	return c.do(ctx, method, path, query, body, "application/json", out)
}

// Health reports whether the node is up and ready.
func (c *Client) Health(ctx context.Context) error {
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.json(ctx, http.MethodGet, "/health", nil, nil, &out); err != nil {
		return err
	}
	if !out.OK {
		return errors.New("typesense is not ready")
	}
	return nil
}

// Field is one field of a collection schema.
type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Facet    bool   `json:"facet,omitempty"`
	Optional bool   `json:"optional,omitempty"`
	Sort     *bool  `json:"sort,omitempty"`
	// Infix lets a query match in the middle of a word ("kilo" in "Cuci Kiloan"), as the Postgres substring search did.
	Infix bool `json:"infix,omitempty"`
}

// Schema is a collection's definition.
type Schema struct {
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
	// DefaultSortingField is left empty: every list asks for its own order.
	DefaultSortingField string `json:"default_sorting_field,omitempty"`
}

// CreateCollection creates a collection.
func (c *Client) CreateCollection(ctx context.Context, s Schema) error {
	return c.json(ctx, http.MethodPost, "/collections", nil, s, nil)
}

// DeleteCollection drops a collection; one that is already gone is not an error.
func (c *Client) DeleteCollection(ctx context.Context, name string) error {
	err := c.json(ctx, http.MethodDelete, "/collections/"+url.PathEscape(name), nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// CollectionExists says whether a collection (not an alias) exists.
func (c *Client) CollectionExists(ctx context.Context, name string) (bool, error) {
	err := c.json(ctx, http.MethodGet, "/collections/"+url.PathEscape(name), nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// AliasTarget returns the collection an alias points at, or "" when there is no such alias.
func (c *Client) AliasTarget(ctx context.Context, alias string) (string, error) {
	var out struct {
		Collection string `json:"collection_name"`
	}
	err := c.json(ctx, http.MethodGet, "/aliases/"+url.PathEscape(alias), nil, nil, &out)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	return out.Collection, err
}

// SetAlias points alias at collection, creating or moving it.
func (c *Client) SetAlias(ctx context.Context, alias, collection string) error {
	return c.json(ctx, http.MethodPut, "/aliases/"+url.PathEscape(alias), nil, map[string]string{"collection_name": collection}, nil)
}

// DeleteAlias removes an alias; one that is already gone is not an error.
func (c *Client) DeleteAlias(ctx context.Context, alias string) error {
	err := c.json(ctx, http.MethodDelete, "/aliases/"+url.PathEscape(alias), nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Upsert writes one document (its "id" field is the id as a string).
func (c *Client) Upsert(ctx context.Context, collection string, doc map[string]any) error {
	return c.json(ctx, http.MethodPost, "/collections/"+url.PathEscape(collection)+"/documents", url.Values{"action": {"upsert"}}, doc, nil)
}

// Delete removes one document; one that is already gone is not an error.
func (c *Client) Delete(ctx context.Context, collection, id string) error {
	err := c.json(ctx, http.MethodDelete, "/collections/"+url.PathEscape(collection)+"/documents/"+url.PathEscape(id), nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Import upserts many documents at once and reports the first one the node refused.
func (c *Client) Import(ctx context.Context, collection string, docs []map[string]any) error {
	if len(docs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return err
		}
	}
	var raw json.RawMessage
	err := c.do(ctx, http.MethodPost, "/collections/"+url.PathEscape(collection)+"/documents/import", url.Values{"action": {"upsert"}}, &buf, "text/plain", &raw)
	if err != nil {
		return err
	}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		var r struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal(line, &r); err != nil {
			return fmt.Errorf("typesense import: unreadable answer: %w", err)
		}
		if !r.Success {
			return fmt.Errorf("typesense import: %s", r.Error)
		}
	}
	return nil
}

// ExportIDs returns the id of every document in a collection.
func (c *Client) ExportIDs(ctx context.Context, collection string) ([]string, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/collections/"+url.PathEscape(collection)+"/documents/export", url.Values{"include_fields": {"id"}}, nil, "", &raw); err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var d struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(line, &d); err != nil {
			return nil, err
		}
		ids = append(ids, d.ID)
	}
	return ids, nil
}

// Count returns how many documents a collection holds.
func (c *Client) Count(ctx context.Context, collection string) (int64, error) {
	var out struct {
		Found int64 `json:"found"`
	}
	err := c.json(ctx, http.MethodGet, "/collections/"+url.PathEscape(collection)+"/documents/search", url.Values{"q": {"*"}, "per_page": {"0"}}, nil, &out)
	return out.Found, err
}
