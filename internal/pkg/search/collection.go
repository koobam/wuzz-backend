package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Collection is one searchable list: a versioned Typesense collection (orders_v1) read and written through an alias (orders), so a
// change of schema is a rebuild into a new collection and a switch of the alias, never an edit in place. It is the shape every
// service's index is made of.
type Collection struct {
	c       *Client
	log     *slog.Logger
	alias   string
	version int
	fields  []Field
}

// NewCollection describes a collection; nothing is created until Ensure or Rebuild.
func NewCollection(c *Client, log *slog.Logger, alias string, version int, fields []Field) *Collection {
	return &Collection{c: c, log: log, alias: alias, version: version, fields: fields}
}

// Name is the versioned collection behind the alias.
func (x *Collection) Name() string { return fmt.Sprintf("%s_v%d", x.alias, x.version) }

// Alias is the name searches and writes go through.
func (x *Collection) Alias() string { return x.alias }

// Source reads every document of the collection from the source of truth.
type Source func(ctx context.Context) ([]map[string]any, error)

// Search runs q. A failure is an Unavailable error, never an empty answer.
func (x *Collection) Search(ctx context.Context, q Query) (Result, error) {
	return x.c.Search(ctx, x.alias, q)
}

// Put writes one document and Remove deletes one, right after Postgres commits. A failure is logged loudly and returned for the
// caller to decide; the periodic reconcile repairs whatever a failed write left behind.
func (x *Collection) Put(ctx context.Context, doc map[string]any) error {
	if err := x.c.Upsert(ctx, x.alias, doc); err != nil {
		x.log.Error("search index write failed; the reconcile will repair it", slog.String("collection", x.alias), slog.Any("id", doc["id"]), slog.Any("error", err))
		return err
	}
	return nil
}

func (x *Collection) Remove(ctx context.Context, id string) error {
	if err := x.c.Delete(ctx, x.alias, id); err != nil {
		x.log.Error("search index delete failed; the reconcile will repair it", slog.String("collection", x.alias), slog.String("id", id), slog.Any("error", err))
		return err
	}
	return nil
}

// Ensure makes sure the collection exists at the current version and is filled: an alias that is missing, points at an older
// version or holds no documents is rebuilt from the source. It is what runs when a service starts.
func (x *Collection) Ensure(ctx context.Context, src Source) error {
	target, err := x.c.AliasTarget(ctx, x.alias)
	if err != nil {
		return err
	}
	// A live rebuild leaves the alias on the "_rebuild" twin of the same version; that is current too.
	if target == x.Name() || target == x.Name()+"_rebuild" {
		if n, err := x.c.Count(ctx, x.alias); err == nil && n > 0 {
			return nil
		}
	}
	return x.Rebuild(ctx, src)
}

// Rebuild fills a fresh collection from the source, points the alias at it, and drops the one it replaces. Searches keep reading the
// old collection until the alias moves.
func (x *Collection) Rebuild(ctx context.Context, src Source) error {
	docs, err := src(ctx)
	if err != nil {
		return err
	}
	old, err := x.c.AliasTarget(ctx, x.alias)
	if err != nil {
		return err
	}
	name := x.Name()
	if old == name {
		// Rebuilding the live version in place: build next to it, then swap.
		name += "_rebuild"
	}
	if err := x.c.DeleteCollection(ctx, name); err != nil {
		return err
	}
	if err := x.c.CreateCollection(ctx, Schema{Name: name, Fields: x.fields}); err != nil {
		return err
	}
	if err := x.c.Import(ctx, name, docs); err != nil {
		return err
	}
	if err := x.c.SetAlias(ctx, x.alias, name); err != nil {
		return err
	}
	if old != "" && old != name {
		if err := x.c.DeleteCollection(ctx, old); err != nil {
			return err
		}
	}
	x.log.Info("search collection rebuilt", slog.String("collection", x.alias), slog.String("name", name), slog.Int("documents", len(docs)))
	return nil
}

// Reconcile brings the live collection in line with the source without moving the alias: every row is upserted, and a document whose
// row is gone is deleted. It repairs what a failed write or a crash in the middle of one left behind.
func (x *Collection) Reconcile(ctx context.Context, src Source) error {
	docs, err := src(ctx)
	if err != nil {
		return err
	}
	if err := x.c.Import(ctx, x.alias, docs); err != nil {
		return err
	}
	have, err := x.c.ExportIDs(ctx, x.alias)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	live := make(map[string]bool, len(docs))
	for _, d := range docs {
		live[fmt.Sprint(d["id"])] = true
	}
	removed := 0
	for _, id := range have {
		if !live[id] {
			if err := x.c.Delete(ctx, x.alias, id); err != nil {
				return err
			}
			removed++
		}
	}
	if removed > 0 {
		x.log.Warn("search reconcile removed documents with no row", slog.String("collection", x.alias), slog.Int("removed", removed))
	}
	return nil
}

// SearchStrings is Search for a collection whose document ids are text (UUIDs).
func (x *Collection) SearchStrings(ctx context.Context, q Query) ([]string, int64, error) {
	return x.c.SearchStrings(ctx, x.alias, q)
}
