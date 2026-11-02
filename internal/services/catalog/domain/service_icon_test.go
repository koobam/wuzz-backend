package domain

import "testing"

func TestServiceIconPicture(t *testing.T) {
	base := func() ServiceItem {
		return ServiceItem{Name: "Cuci Kiloan", Unit: UnitKg, Price: 7000, TaxType: TaxNone, DiscountType: DiscountNone}
	}
	for _, tc := range []struct {
		name    string
		id, url string
		ok      bool
	}{
		{"neither: the app draws the named icon", "", "", true},
		{"both", "6F9619FF-8B86-D011-B42D-00C04FC964FF", "https://assets.wuzz.care/services/2026/11/abc.webp", true},
		{"an id without an address", "6f9619ff-8b86-d011-b42d-00c04fc964ff", "", false},
		{"an address without an id", "", "https://assets.wuzz.care/x.webp", false},
		{"not a media id", "kiloan", "https://assets.wuzz.care/x.webp", false},
		{"not a web address", "6f9619ff-8b86-d011-b42d-00c04fc964ff", "javascript:alert(1)", false},
	} {
		s := base()
		s.IconMediaID, s.IconURL = tc.id, tc.url
		err := s.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
		if err == nil && tc.id != "" && s.IconMediaID != "6f9619ff-8b86-d011-b42d-00c04fc964ff" {
			t.Errorf("%s: the id must be kept lowercase, got %q", tc.name, s.IconMediaID)
		}
	}
}
