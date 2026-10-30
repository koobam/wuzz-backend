package domain

import "testing"

func TestBoundsValidate(t *testing.T) {
	cases := []struct {
		name string
		b    Bounds
		ok   bool
	}{
		{"Jakarta", Bounds{South: -6.4, West: 106.6, North: -6.0, East: 107.0}, true},
		{"the whole world", Bounds{South: -90, West: -180, North: 90, East: 180}, true},
		{"a point", Bounds{South: -6.2, West: 106.8, North: -6.2, East: 106.8}, true},
		{"south above north", Bounds{South: -6.0, West: 106.6, North: -6.4, East: 107.0}, false},
		{"west beyond east", Bounds{South: -6.4, West: 107.0, North: -6.0, East: 106.6}, false},
		{"north off the globe", Bounds{South: 0, West: 0, North: 91, East: 1}, false},
		{"east off the globe", Bounds{South: 0, West: 0, North: 1, East: 181}, false},
	}
	for _, c := range cases {
		if err := c.b.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}
