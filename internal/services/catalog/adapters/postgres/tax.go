package postgres

import (
	"context"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// loadTaxRates reads the rate of every tax type. The table is a handful of rows, so it is read as it is needed.
func loadTaxRates(ctx context.Context, q queryer) (domain.TaxRates, error) {
	rows, err := q.Query(ctx, `SELECT tax_type, rate_bps FROM tax_rates`)
	if err != nil {
		return nil, apperr.Internal("Could not read the tax rates").WithCause(err)
	}
	defer rows.Close()
	rates := domain.TaxRates{}
	for rows.Next() {
		var t string
		var bps int32
		if err := rows.Scan(&t, &bps); err != nil {
			return nil, apperr.Internal("Could not read the tax rates").WithCause(err)
		}
		rates[t] = bps
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("Could not read the tax rates").WithCause(err)
	}
	return rates, nil
}

// TaxRates is the rate of each tax type, for the form that previews a price after tax.
func (s *Store) TaxRates(ctx context.Context) (domain.TaxRates, error) {
	return loadTaxRates(ctx, s.pool)
}

// applyTax fills the discount amount, the rate and the prices (after discount, after tax) of services just read.
func applyTax(ctx context.Context, q queryer, items []domain.ServiceItem) error {
	if len(items) == 0 {
		return nil
	}
	rates, err := loadTaxRates(ctx, q)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].ApplyPricing(rates)
	}
	return nil
}
