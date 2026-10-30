// Package catalogclient is how the order service reaches the catalog: it asks it to take an order's materials out of stock, or to put
// them back. The call is made while the order's status changes, and a failure is the status change's failure.
package catalogclient

import (
	"context"
	"strconv"
	"time"

	"google.golang.org/grpc/metadata"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/transaction/domain"
)

// internalKeyHeader is the metadata key the catalog reads the shared secret from.
const internalKeyHeader = "x-internal-key"

// callTimeout bounds the wait for the catalog, since the order's row stays locked meanwhile.
const callTimeout = 10 * time.Second

type Stock struct {
	catalog catalogv1.CatalogServiceClient
	key     string
}

// New takes the catalog client and the shared secret (INTERNAL_API_KEY) the catalog asks of the services that call it.
func New(catalog catalogv1.CatalogServiceClient, key string) *Stock {
	return &Stock{catalog: catalog, key: key}
}

// ApplyOrderStock asks the catalog to apply action (domain.StockConsume or StockReverse) for the order. The catalog does each action
// once per order, so repeating a call that may or may not have arrived is harmless.
func (s *Stock) ApplyOrderStock(ctx context.Context, action string, o domain.Order, actor string) error {
	if s.key == "" {
		return apperr.Internal("The stock cannot be updated: INTERNAL_API_KEY is not set on the order service")
	}
	req := &catalogv1.ApplyOrderStockRequest{OrderId: o.ID, OrderCode: o.Code, OutletId: o.OutletID, Action: action, Actor: actor}
	for _, l := range o.Lines {
		req.Lines = append(req.Lines, &catalogv1.OrderStockLine{
			ServiceId: l.ItemID,
			// The order keeps a float64; its shortest exact decimal is what the catalog reads ("0.5", "2.5").
			Qty:              strconv.FormatFloat(l.Qty, 'f', -1, 64),
			VariantOptionIds: l.VariantOptionIDs, ModifierOptionIds: l.ModifierOptionIDs,
		})
	}
	ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(ctx, internalKeyHeader, s.key), callTimeout)
	defer cancel()
	if _, err := s.catalog.ApplyOrderStock(ctx, req); err != nil {
		return apperr.Internal("The stock could not be updated, so the order was not changed. Try again in a moment.").WithCause(err)
	}
	return nil
}
