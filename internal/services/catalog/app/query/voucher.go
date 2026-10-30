package query

import (
	"context"
	"time"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Vouchers is what the voucher reads need of the store.
type Vouchers interface {
	ListVouchers(ctx context.Context, q domain.VoucherQuery) ([]domain.Voucher, int64, error)
	GetVoucher(ctx context.Context, id int64) (domain.Voucher, error)
	ListVoucherGrants(ctx context.Context, q domain.GrantQuery) ([]domain.VoucherGrant, int64, error)
	ListInjections(ctx context.Context, q domain.InjectionQuery) ([]domain.Injection, int64, error)
	// ListWallet is what a customer holds at now: the public vouchers that are live, and their own grants of vouchers that are live and
	// not expired.
	ListWallet(ctx context.Context, customerID int64, now time.Time) ([]domain.WalletVoucher, error)
}

// ---------- ListVouchers ----------

type ListVouchers struct{ Query domain.VoucherQuery }

type ListVouchersResult struct {
	Vouchers []domain.Voucher
	Total    int64
	// Now is the clock the list was judged by, so each voucher's liveness is read at the same instant as the filter.
	Now time.Time
}

type ListVouchersHandler struct {
	vouchers Vouchers
	now      func() time.Time
}

func NewListVouchersHandler(vouchers Vouchers) *ListVouchersHandler {
	return &ListVouchersHandler{vouchers: vouchers, now: time.Now}
}

func (h *ListVouchersHandler) Handle(ctx context.Context, q ListVouchers) (ListVouchersResult, error) {
	if err := q.Query.Validate(); err != nil {
		return ListVouchersResult{}, err
	}
	q.Query.Now = h.now()
	items, total, err := h.vouchers.ListVouchers(ctx, q.Query)
	return ListVouchersResult{Vouchers: items, Total: total, Now: q.Query.Now}, err
}

// ---------- GetVoucher ----------

type GetVoucher struct{ ID int64 }

type GetVoucherHandler struct{ vouchers Vouchers }

func NewGetVoucherHandler(vouchers Vouchers) *GetVoucherHandler {
	return &GetVoucherHandler{vouchers: vouchers}
}

func (h *GetVoucherHandler) Handle(ctx context.Context, q GetVoucher) (domain.Voucher, error) {
	return h.vouchers.GetVoucher(ctx, q.ID)
}

// ---------- ListVoucherGrants ----------

type ListVoucherGrants struct{ Query domain.GrantQuery }

type ListVoucherGrantsResult struct {
	Grants []domain.VoucherGrant
	Total  int64
}

type ListVoucherGrantsHandler struct{ vouchers Vouchers }

func NewListVoucherGrantsHandler(vouchers Vouchers) *ListVoucherGrantsHandler {
	return &ListVoucherGrantsHandler{vouchers: vouchers}
}

func (h *ListVoucherGrantsHandler) Handle(ctx context.Context, q ListVoucherGrants) (ListVoucherGrantsResult, error) {
	if err := q.Query.Validate(); err != nil {
		return ListVoucherGrantsResult{}, err
	}
	// An unknown voucher is a 404, not an empty page.
	if _, err := h.vouchers.GetVoucher(ctx, q.Query.VoucherID); err != nil {
		return ListVoucherGrantsResult{}, err
	}
	items, total, err := h.vouchers.ListVoucherGrants(ctx, q.Query)
	return ListVoucherGrantsResult{Grants: items, Total: total}, err
}

// ---------- ListInjections ----------

type ListInjections struct{ Query domain.InjectionQuery }

type ListInjectionsResult struct {
	Injections []domain.Injection
	Total      int64
}

type ListInjectionsHandler struct{ vouchers Vouchers }

func NewListInjectionsHandler(vouchers Vouchers) *ListInjectionsHandler {
	return &ListInjectionsHandler{vouchers: vouchers}
}

func (h *ListInjectionsHandler) Handle(ctx context.Context, q ListInjections) (ListInjectionsResult, error) {
	if err := q.Query.Validate(); err != nil {
		return ListInjectionsResult{}, err
	}
	items, total, err := h.vouchers.ListInjections(ctx, q.Query)
	return ListInjectionsResult{Injections: items, Total: total}, err
}

// ---------- ListWallet ----------

type ListWallet struct{ CustomerID int64 }

type ListWalletHandler struct {
	vouchers Vouchers
	now      func() time.Time
}

func NewListWalletHandler(vouchers Vouchers) *ListWalletHandler {
	return &ListWalletHandler{vouchers: vouchers, now: time.Now}
}

func (h *ListWalletHandler) Handle(ctx context.Context, q ListWallet) ([]domain.WalletVoucher, error) {
	return h.vouchers.ListWallet(ctx, q.CustomerID, h.now())
}
