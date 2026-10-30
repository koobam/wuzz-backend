package domain

import "testing"

const uid = "6f9619ff-8b86-d011-b42d-00cf4fc964ff"

func TestOrderKeepsVariantAndModifiers(t *testing.T) {
	lines := []Line{{ItemID: 8, Name: "Kiloan", Unit: "kg", Price: 13500, Qty: 2, Variant: " Express ",
		Modifiers: []Modifier{{Group: "Tambahan", Name: " Packing ", Price: 3000}}}}
	o, err := NewOrder(uid, 1, "Tes", "", lines, "s:1")
	if err != nil {
		t.Fatal(err)
	}
	l := o.Lines[0]
	if l.Variant != "Express" || l.Modifiers[0].Name != "Packing" || o.Total != 27000 {
		t.Errorf("line = %+v, total = %d", l, o.Total)
	}
}

func TestOrderRejectsBadChoices(t *testing.T) {
	cases := map[string][]Modifier{
		"blank name":     {{Name: " "}},
		"negative price": {{Name: "x", Price: -1}},
		"too many":       make([]Modifier, maxModifiers+1),
	}
	for name, mods := range cases {
		for i := range mods {
			if name == "too many" {
				mods[i] = Modifier{Name: "x"}
			}
		}
		_, err := NewOrder(uid, 1, "Tes", "", []Line{{Name: "K", Price: 1, Qty: 1, Modifiers: mods}}, "s:1")
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
