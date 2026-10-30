package command

import (
	"context"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Vouchers is what the voucher writes need of the store.
type Vouchers interface {
	// CreateVoucher refuses a code that is taken and an outlet or service that does not exist.
	CreateVoucher(ctx context.Context, v domain.Voucher) (domain.Voucher, error)
	UpdateVoucher(ctx context.Context, v domain.Voucher) (domain.Voucher, error)
	SetVoucherStatus(ctx context.Context, id int64, status string) (domain.Voucher, error)
	// DeleteVoucher refuses a voucher that customers hold.
	DeleteVoucher(ctx context.Context, id int64) error
	// Inject puts the voucher in the wallets of the customers and writes the audit of it; it decides who gets one with domain.PlanInjection
	// under a lock on the voucher, so two injections cannot take the same last claim.
	Inject(ctx context.Context, in domain.InjectInput, actor Actor, now time.Time) (domain.Injection, error)
	// ClaimVoucher puts the voucher with this code in the customer's wallet, if it can be had now.
	ClaimVoucher(ctx context.Context, in domain.ClaimInput, now time.Time) (domain.WalletVoucher, error)
}

// Actor is the admin who did something, for the audit.
type Actor struct{ ID, Name string }

// ---------- CreateVoucher ----------

type CreateVoucher struct{ Input domain.VoucherInput }

type CreateVoucherHandler struct{ vouchers Vouchers }

func NewCreateVoucherHandler(vouchers Vouchers) *CreateVoucherHandler {
	return &CreateVoucherHandler{vouchers: vouchers}
}

func (h *CreateVoucherHandler) Handle(ctx context.Context, cmd CreateVoucher) (domain.Voucher, error) {
	v, err := domain.ParseVoucher(cmd.Input)
	if err != nil {
		return domain.Voucher{}, err
	}
	return h.vouchers.CreateVoucher(ctx, v)
}

// ---------- UpdateVoucher ----------

type UpdateVoucher struct {
	ID    int64
	Input domain.VoucherInput
}

type UpdateVoucherHandler struct{ vouchers Vouchers }

func NewUpdateVoucherHandler(vouchers Vouchers) *UpdateVoucherHandler {
	return &UpdateVoucherHandler{vouchers: vouchers}
}

func (h *UpdateVoucherHandler) Handle(ctx context.Context, cmd UpdateVoucher) (domain.Voucher, error) {
	v, err := domain.ParseVoucher(cmd.Input)
	if err != nil {
		return domain.Voucher{}, err
	}
	v.ID = cmd.ID
	return h.vouchers.UpdateVoucher(ctx, v)
}

// ---------- SetVoucherStatus ----------

type SetVoucherStatus struct {
	ID     int64
	Status string
}

type SetVoucherStatusHandler struct{ vouchers Vouchers }

func NewSetVoucherStatusHandler(vouchers Vouchers) *SetVoucherStatusHandler {
	return &SetVoucherStatusHandler{vouchers: vouchers}
}

func (h *SetVoucherStatusHandler) Handle(ctx context.Context, cmd SetVoucherStatus) (domain.Voucher, error) {
	if cmd.Status != domain.VoucherActive && cmd.Status != domain.VoucherInactive {
		return domain.Voucher{}, apperr.Invalid("The status must be active or inactive")
	}
	return h.vouchers.SetVoucherStatus(ctx, cmd.ID, cmd.Status)
}

// ---------- DeleteVoucher ----------

type DeleteVoucher struct{ ID int64 }

type DeleteVoucherHandler struct{ vouchers Vouchers }

func NewDeleteVoucherHandler(vouchers Vouchers) *DeleteVoucherHandler {
	return &DeleteVoucherHandler{vouchers: vouchers}
}

func (h *DeleteVoucherHandler) Handle(ctx context.Context, cmd DeleteVoucher) error {
	return h.vouchers.DeleteVoucher(ctx, cmd.ID)
}

// ---------- InjectVoucher ----------

type InjectVoucher struct {
	Input domain.InjectInput
	Actor Actor
}

type InjectVoucherHandler struct {
	vouchers Vouchers
	now      func() time.Time
}

func NewInjectVoucherHandler(vouchers Vouchers) *InjectVoucherHandler {
	return &InjectVoucherHandler{vouchers: vouchers, now: time.Now}
}

func (h *InjectVoucherHandler) Handle(ctx context.Context, cmd InjectVoucher) (domain.Injection, error) {
	now := h.now()
	if err := cmd.Input.Validate(now); err != nil {
		return domain.Injection{}, err
	}
	return h.vouchers.Inject(ctx, cmd.Input, cmd.Actor, now)
}

// ---------- ClaimVoucher ----------

type ClaimVoucher struct{ Input domain.ClaimInput }

type ClaimVoucherHandler struct {
	vouchers Vouchers
	now      func() time.Time
}

func NewClaimVoucherHandler(vouchers Vouchers) *ClaimVoucherHandler {
	return &ClaimVoucherHandler{vouchers: vouchers, now: time.Now}
}

func (h *ClaimVoucherHandler) Handle(ctx context.Context, cmd ClaimVoucher) (domain.WalletVoucher, error) {
	code, err := domain.NormaliseCode(cmd.Input.Code)
	if err != nil {
		return domain.WalletVoucher{}, err
	}
	cmd.Input.Code = code
	return h.vouchers.ClaimVoucher(ctx, cmd.Input, h.now())
}
