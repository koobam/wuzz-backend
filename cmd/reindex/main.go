// Command reindex rebuilds a service's Typesense collections from Postgres and exits. Postgres is the source of truth, so a rebuild
// is always safe: a new collection is filled, the alias is switched to it, and the old one is dropped; searches keep working
// throughout. A service does the same by itself when it starts and finds a collection missing, empty or at an older schema version;
// this is for the other times (Typesense restored from nothing, a bulk change made straight in the database).
//
//	reindex catalog [collection...]   outlets, services, materials, variant_groups, modifier_groups, banners, vouchers (default: all)
//	reindex transaction               orders
//
// The database and Typesense are read from the same variables the service itself reads (POSTGRES_DSN, TYPESENSE_URL,
// TYPESENSE_API_KEY); `make reindex SERVICE=catalog` loads .env first.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/observability"
	"github.com/koobam/wuzz-backend/internal/pkg/search"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
	catalogpg "github.com/koobam/wuzz-backend/internal/services/catalog/adapters/postgres"
	catalogindex "github.com/koobam/wuzz-backend/internal/services/catalog/adapters/searchindex"
	txpg "github.com/koobam/wuzz-backend/internal/services/transaction/adapters/postgres"
	txindex "github.com/koobam/wuzz-backend/internal/services/transaction/adapters/searchindex"
)

func main() {
	if err := run(); err != nil {
		slog.Error("reindex failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: reindex catalog|transaction [collection...]")
	}
	service := config.Service(os.Args[1])
	ctx := context.Background()

	cfg, err := config.Load(service)
	if err != nil {
		return err
	}
	log := observability.NewLogger(cfg.Telemetry.LogLevel, cfg.Telemetry.LogFormat, "reindex", "cli")
	client := search.New(cfg.Typesense.URL, cfg.Typesense.APIKey)
	if !client.Enabled() {
		return fmt.Errorf("TYPESENSE_URL is not set: there is no index to rebuild")
	}
	if err := client.Health(ctx); err != nil {
		return fmt.Errorf("typesense is not reachable at %s: %w", cfg.Typesense.URL, err)
	}
	pool, err := postgres.New(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch service {
	case config.ServiceCatalog:
		store := catalogpg.NewStore(pool)
		ix := catalogindex.New(client, log)
		var only []catalogindex.Kind
		for _, a := range os.Args[2:] {
			k := catalogindex.Kind(a)
			if !known(k) {
				return fmt.Errorf("unknown collection %q (one of %v)", a, catalogindex.Kinds)
			}
			only = append(only, k)
		}
		return ix.Rebuild(ctx, store.Everything, only...)
	case config.ServiceTransaction:
		store := txpg.NewStore(pool)
		return txindex.New(client, log).Rebuild(ctx, store.Everything)
	}
	return fmt.Errorf("reindex catalog|transaction, not %q", os.Args[1])
}

func known(k catalogindex.Kind) bool {
	for _, c := range catalogindex.Kinds {
		if c == k {
			return true
		}
	}
	return false
}
