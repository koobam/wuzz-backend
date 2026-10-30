package orders

import (
	"testing"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
)

func TestPinnedOutlet(t *testing.T) {
	cases := []struct {
		name  string
		roles []string
		want  int64
	}{
		{"front staff", []string{"outlet:3"}, 3},
		{"an admin assigned to an outlet", []string{"admin", "admin:super", "outlet:1"}, 1},
		{"an admin with no outlet pins nothing", []string{"admin", "admin:super"}, 0},
		{"two outlets pin nothing", []string{"outlet:1", "outlet:2"}, 0},
		{"the same outlet twice is still one", []string{"outlet:2", "outlet:2"}, 2},
		{"a malformed outlet is ignored", []string{"outlet:x", "outlet:4"}, 4},
		{"a wuzzer has none", []string{"wuzzer"}, 0},
	}
	for _, c := range cases {
		if got := PinnedOutlet(authn.Identity{Roles: c.roles}); got != c.want {
			t.Errorf("%s: PinnedOutlet(%v) = %d, want %d", c.name, c.roles, got, c.want)
		}
	}
}
