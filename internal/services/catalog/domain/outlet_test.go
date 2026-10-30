package domain

import (
	"strings"
	"testing"
)

func TestOutletImages(t *testing.T) {
	base := Outlet{Name: "Wuzz Pusat", Latitude: -6.2, Longitude: 106.8, Category: "all", DeliveryTypes: []string{"pickup"}, OpenTime: "08:00", CloseTime: "22:00"}
	cases := []struct {
		name, image string
		ok          bool
	}{
		{"none", "", true},
		{"https", "https://assets.wuzz.care/outlets/pusat.jpg", true},
		{"padded", "  https://assets.wuzz.care/a.jpg  ", true},
		{"plain http", "http://assets.wuzz.care/a.jpg", false},
		{"http on localhost (dev media)", "http://localhost:8092/media/outlets/2026/10/a.jpg", true},
		{"http on 127.0.0.1", "http://127.0.0.1:8092/media/a.jpg", true},
		{"http on a lookalike host", "http://localhost.evil.example/a.jpg", false},
		{"ftp", "ftp://localhost/a.jpg", false},
		{"a script", "javascript:alert(1)", false},
		{"a data uri", "data:image/png;base64,AAAA", false},
		{"no host", "https://", false},
		{"not a url", "pusat.jpg", false},
		{"too long", "https://assets.wuzz.care/" + strings.Repeat("a", 500), false},
	}
	for _, c := range cases {
		o := base
		if c.image != "" {
			o.Images = []string{c.image}
		}
		if err := o.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", c.name, err, c.ok)
		}
	}
	o := base
	o.Images = []string{"  https://assets.wuzz.care/a.jpg "}
	_ = o.Validate()
	if o.Images[0] != "https://assets.wuzz.care/a.jpg" {
		t.Errorf("the address should be trimmed, got %q", o.Images[0])
	}
	many := base
	for i := 0; i < 11; i++ {
		many.Images = append(many.Images, "https://assets.wuzz.care/a.jpg")
	}
	if many.Validate() == nil {
		t.Error("eleven pictures should be refused")
	}
}
