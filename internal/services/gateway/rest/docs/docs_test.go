package docs

import "testing"

func TestSpecIsValidYAML(t *testing.T) {
	ops, err := Operations()
	if err != nil {
		t.Fatalf("the spec does not parse: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("the spec documents no operations")
	}
}
