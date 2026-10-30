package catalogapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	transactionv1 "github.com/koobam/wuzz-backend/gen/wuzz/transaction/v1"
	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
)

// The three upstreams the delete talks to, each answering from a table.
type deleteCatalog struct {
	catalogv1.CatalogServiceClient
	known   bool
	deleted []int64
	err     error
}

func (f *deleteCatalog) GetOutlet(context.Context, *catalogv1.GetOutletRequest, ...grpc.CallOption) (*catalogv1.GetOutletResponse, error) {
	if !f.known {
		return nil, status.Error(codes.NotFound, "Outlet not found")
	}
	return &catalogv1.GetOutletResponse{Outlet: &catalogv1.Outlet{}}, nil
}
func (f *deleteCatalog) DeleteOutlet(_ context.Context, r *catalogv1.DeleteOutletRequest, _ ...grpc.CallOption) (*catalogv1.DeleteOutletResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.deleted = append(f.deleted, r.GetId())
	return &catalogv1.DeleteOutletResponse{}, nil
}
func (f *deleteCatalog) DeleteService(_ context.Context, r *catalogv1.DeleteServiceRequest, _ ...grpc.CallOption) (*catalogv1.DeleteServiceResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.deleted = append(f.deleted, r.GetId())
	return &catalogv1.DeleteServiceResponse{}, nil
}

type deleteUsers struct {
	userv1.UserServiceClient
	totals map[string]int64 // by kind
}

func (f deleteUsers) ListAccounts(_ context.Context, r *userv1.ListAccountsRequest, _ ...grpc.CallOption) (*userv1.ListAccountsResponse, error) {
	return &userv1.ListAccountsResponse{Total: f.totals[r.GetKind()]}, nil
}

type deleteOrders struct {
	transactionv1.TransactionServiceClient
	total int64
}

func (f deleteOrders) ListOrders(context.Context, *transactionv1.ListOrdersRequest, ...grpc.CallOption) (*transactionv1.ListOrdersResponse, error) {
	return &transactionv1.ListOrdersResponse{Total: f.total}, nil
}

func TestDeleteOutletChecksWhatPointsAtIt(t *testing.T) {
	cases := []struct {
		name     string
		known    bool
		staff    int64
		admins   int64
		orders   int64
		noUpstr  bool
		wantCode int
		wantText string
		deleted  bool
	}{
		{name: "nothing points at it", known: true, wantCode: 200, deleted: true},
		{name: "staff", known: true, staff: 2, wantCode: 409, wantText: "2 staff accounts"},
		{name: "one admin", known: true, admins: 1, wantCode: 409, wantText: "1 admin account and"},
		{name: "orders, singular", known: true, orders: 1, wantCode: 409, wantText: "1 order and cannot"},
		{name: "everything is named", known: true, staff: 1, admins: 1, orders: 5, wantCode: 409, wantText: "1 staff account, 1 admin account, 5 orders"},
		{name: "an unknown outlet is a 404", known: false, wantCode: 404},
		{name: "without the other services it refuses rather than guess", known: true, noUpstr: true, wantCode: 500},
	}
	for _, c := range cases {
		cat := &deleteCatalog{known: c.known}
		h := &Handler{Catalog: cat}
		if !c.noUpstr {
			h.User = deleteUsers{totals: map[string]int64{"staff": c.staff, "admin": c.admins}}
			h.Tx = deleteOrders{total: c.orders}
		}
		r := router("admin")
		r.DELETE("/outlets/:id", h.BODeleteOutlet)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/outlets/7", nil))
		if rec.Code != c.wantCode || !strings.Contains(rec.Body.String(), c.wantText) {
			t.Errorf("%s: %d %s; want %d containing %q", c.name, rec.Code, rec.Body.String(), c.wantCode, c.wantText)
		}
		if got := len(cat.deleted) == 1; got != c.deleted {
			t.Errorf("%s: deleted = %v; want %v", c.name, cat.deleted, c.deleted)
		}
	}
}

func TestDeleteOutletPassesTheCatalogsRefusalOn(t *testing.T) {
	cat := &deleteCatalog{known: true, err: status.Error(codes.AlreadyExists, "This outlet has stock records and cannot be deleted; switch it off instead")}
	h := &Handler{Catalog: cat, User: deleteUsers{}, Tx: deleteOrders{}}
	r := router("admin")
	r.DELETE("/outlets/:id", h.BODeleteOutlet)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/outlets/7", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "stock records") {
		t.Errorf("%d %s; want the catalog's 409", rec.Code, rec.Body.String())
	}
}

func TestDeleteServiceAndBadIDs(t *testing.T) {
	cat := &deleteCatalog{known: true}
	h := &Handler{Catalog: cat}
	r := router("admin")
	r.DELETE("/services/:id", h.BODeleteService)
	r.DELETE("/outlets/:id", h.BODeleteOutlet)
	for path, want := range map[string]int{"/services/12": 200, "/services/abc": 400, "/services/0": 400, "/outlets/-1": 400} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
		if rec.Code != want {
			t.Errorf("DELETE %s = %d; want %d", path, rec.Code, want)
		}
	}
	if len(cat.deleted) != 1 || cat.deleted[0] != 12 {
		t.Errorf("deleted = %v; want [12]", cat.deleted)
	}
}
