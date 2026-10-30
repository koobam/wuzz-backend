package command

import (
	"context"
	"testing"

	"github.com/koobam/wuzz-backend/internal/services/catalog/domain"
)

// Each fake embeds the port it stands in for, so only the methods a test uses need writing; reaching any other one
// panics on the nil interface, which is what "the store was never meant to be touched" should do.

type outletFake struct {
	Outlets
	created, updated []domain.Outlet
}

func (f *outletFake) CreateOutlet(_ context.Context, o domain.Outlet) (domain.Outlet, error) {
	f.created = append(f.created, o)
	return o, nil
}

func (f *outletFake) UpdateOutlet(_ context.Context, o domain.Outlet) (domain.Outlet, error) {
	f.updated = append(f.updated, o)
	return o, nil
}

func validOutlet() domain.Outlet {
	return domain.Outlet{Name: "Wuzz Pusat", Address: "Jl. Contoh No. 1", Phone: "0211234567", Email: "pusat@wuzz.care", Latitude: -6.2, Longitude: 106.8, Category: "all", DeliveryTypes: []string{"pickup", "delivery"}, OpenTime: "08:00", CloseTime: "22:00"}
}

func TestCreateOutletIsAlwaysActiveAndValidated(t *testing.T) {
	f := &outletFake{}
	h := NewCreateOutletHandler(f)

	o := validOutlet()
	o.Active = false
	if _, err := h.Handle(context.Background(), CreateOutlet{Outlet: o}); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 || !f.created[0].Active {
		t.Errorf("a new outlet must be stored active, got %+v", f.created)
	}

	bad := validOutlet()
	bad.Name = "  "
	if _, err := h.Handle(context.Background(), CreateOutlet{Outlet: bad}); err == nil {
		t.Error("an outlet with no name was accepted")
	}
	if len(f.created) != 1 {
		t.Errorf("an invalid outlet reached the store: %d writes", len(f.created))
	}
}

func TestUpdateOutletKeepsWhatTheCallerSaidAndValidates(t *testing.T) {
	f := &outletFake{}
	h := NewUpdateOutletHandler(f)

	o := validOutlet()
	o.ID, o.Active = 7, false
	if _, err := h.Handle(context.Background(), UpdateOutlet{Outlet: o}); err != nil {
		t.Fatal(err)
	}
	if len(f.updated) != 1 || f.updated[0].Active || f.updated[0].ID != 7 {
		t.Errorf("an update must keep the id and the caller's active flag, got %+v", f.updated)
	}

	bad := validOutlet()
	bad.Latitude = 200
	if _, err := h.Handle(context.Background(), UpdateOutlet{Outlet: bad}); err == nil {
		t.Error("an outlet off the globe was accepted")
	}
	if len(f.updated) != 1 {
		t.Errorf("an invalid outlet reached the store: %d writes", len(f.updated))
	}
}

func TestCreateOutletTakesTheDefaultsForWhatIsLeftOut(t *testing.T) {
	f := &outletFake{}
	h := NewCreateOutletHandler(f)
	if _, err := h.Handle(context.Background(), CreateOutlet{Outlet: domain.Outlet{Name: "Wuzz Baru", Phone: "0211234567", Email: "baru@wuzz.care", Category: "all", Latitude: -6.2, Longitude: 106.8}}); err != nil {
		t.Fatal(err)
	}
	got := f.created[0]
	if got.Category != "all" || got.OpenTime != "08:00" || got.CloseTime != "22:00" || len(got.DeliveryTypes) != 2 {
		t.Errorf("a new outlet that said nothing about its hours should get the defaults, got %+v", got)
	}
}

func TestUpdateOutletRefusesBadHours(t *testing.T) {
	f := &outletFake{}
	h := NewUpdateOutletHandler(f)
	for _, c := range []struct{ open, close string }{{"08:00", "08:00"}, {"8am", "22:00"}, {"08:00", "24:00"}, {"", "22:00"}} {
		o := validOutlet()
		o.ID, o.OpenTime, o.CloseTime = 7, c.open, c.close
		if _, err := h.Handle(context.Background(), UpdateOutlet{Outlet: o}); err == nil {
			t.Errorf("hours %q-%q were accepted", c.open, c.close)
		}
	}
	if len(f.updated) != 0 {
		t.Errorf("bad hours reached the store: %d writes", len(f.updated))
	}
}

func TestCreateOutletRequiresPhoneEmailAndCategory(t *testing.T) {
	f := &outletFake{}
	h := NewCreateOutletHandler(f)
	for name, mutate := range map[string]func(*domain.Outlet){
		"no phone":    func(o *domain.Outlet) { o.Phone = "  " },
		"bad phone":   func(o *domain.Outlet) { o.Phone = "call me" },
		"no email":    func(o *domain.Outlet) { o.Email = "" },
		"bad email":   func(o *domain.Outlet) { o.Email = "Wuzz <a@b.co>" },
		"no category": func(o *domain.Outlet) { o.Category = "" },
	} {
		o := validOutlet()
		mutate(&o)
		if _, err := h.Handle(context.Background(), CreateOutlet{Outlet: o}); err == nil {
			t.Errorf("%s: a new outlet was accepted", name)
		}
	}
	if len(f.created) != 0 {
		t.Errorf("an outlet missing its contact reached the store: %d writes", len(f.created))
	}
}
