package search

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Query is one search: free text over some fields, filters, an order and a page.
type Query struct {
	// Q is the text to find; "" matches everything.
	Q string
	// QueryBy are the text fields Q is matched against, best first.
	QueryBy []string
	// Filters are ANDed. Build them with Eq, In, Gte and Lte: they quote what they are given.
	Filters []string
	// Sort is "field:asc|desc" entries, at most three; the caller ends with an id tie-break so pages never overlap.
	Sort          []string
	Limit, Offset int
}

// Result is the ids of the page in order, and how many documents matched in all.
type Result struct {
	IDs   []int64
	Total int64
}

// Search runs q against a collection (or an alias) whose document ids are numbers. A transport failure or a refusal is an
// Unavailable error.
func (c *Client) Search(ctx context.Context, collection string, q Query) (Result, error) {
	ids, total, err := c.SearchStrings(ctx, collection, q)
	if err != nil {
		return Result{}, err
	}
	out := make([]int64, 0, len(ids))
	for _, s := range ids {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return Result{}, Unavailable(err)
		}
		out = append(out, id)
	}
	return Result{IDs: out, Total: total}, nil
}

// SearchStrings is Search for a collection whose document ids are text (UUIDs): the ids of the page in order, and the total.
func (c *Client) SearchStrings(ctx context.Context, collection string, q Query) ([]string, int64, error) {
	v := url.Values{}
	text := strings.TrimSpace(q.Q)
	if text == "" {
		v.Set("q", "*")
	} else {
		v.Set("q", text)
		v.Set("query_by", strings.Join(q.QueryBy, ","))
		// Match inside words like the old substring search, and forgive one typo in a word of five letters or more.
		v.Set("infix", strings.TrimSuffix(strings.Repeat("always,", len(q.QueryBy)), ","))
		v.Set("num_typos", "1")
		v.Set("min_len_1typo", "5")
		v.Set("prefix", "true")
		v.Set("drop_tokens_threshold", "0")
	}
	if len(q.Filters) > 0 {
		v.Set("filter_by", strings.Join(q.Filters, " && "))
	}
	if len(q.Sort) > 0 {
		v.Set("sort_by", strings.Join(q.Sort, ","))
	}
	v.Set("limit", strconv.Itoa(q.Limit))
	v.Set("offset", strconv.Itoa(q.Offset))
	v.Set("include_fields", "id")

	var out struct {
		Found int64 `json:"found"`
		Hits  []struct {
			Document struct {
				ID string `json:"id"`
			} `json:"document"`
		} `json:"hits"`
	}
	if err := c.json(ctx, http.MethodGet, "/collections/"+url.PathEscape(collection)+"/documents/search", v, nil, &out); err != nil {
		return nil, 0, Unavailable(err)
	}
	ids := make([]string, 0, len(out.Hits))
	for _, h := range out.Hits {
		ids = append(ids, h.Document.ID)
	}
	return ids, out.Found, nil
}

// quote makes a value safe inside a filter_by: Typesense reads backtick-quoted text literally, so the only characters that
// could break out of it are the backtick itself and a line break, which are dropped.
func quote(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '`' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
	return "`" + s + "`"
}

// Eq is `field:=value` for a text value.
func Eq(field, value string) string { return field + ":=" + quote(value) }

// EqInt is `field:=n`.
func EqInt(field string, n int64) string { return field + ":=" + strconv.FormatInt(n, 10) }

// EqBool is `field:=true|false`.
func EqBool(field string, b bool) string { return field + ":=" + strconv.FormatBool(b) }

// Gte is `field:>=n`; Lte is `field:<=n`.
func Gte(field string, n int64) string { return field + ":>=" + strconv.FormatInt(n, 10) }
func Lte(field string, n int64) string { return field + ":<=" + strconv.FormatInt(n, 10) }

// Sort is `field:asc|desc`; anything but "desc" is ascending.
func Sort(field, dir string) string {
	if dir == "desc" {
		return field + ":desc"
	}
	return field + ":asc"
}

// InInt is `field:=[a,b,c]`: the field is any of these numbers.
func InInt(field string, ns []int64) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.FormatInt(n, 10)
	}
	return field + ":=[" + strings.Join(parts, ",") + "]"
}
