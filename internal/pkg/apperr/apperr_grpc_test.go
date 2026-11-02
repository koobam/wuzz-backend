package apperr

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCStatusKeepsTheKind(t *testing.T) {
	for _, e := range []*Error{TooLarge("big"), Unsupported("gif"), Invalid("bad"), NotFound("gone"), Unavailable("down")} {
		st, _ := status.FromError(e.GRPCStatus().Err())
		back := FromGRPCStatus(st)
		if back.Kind != e.Kind || back.Message != e.Message || back.HTTPStatus() != e.HTTPStatus() {
			t.Errorf("%s came back as %s %q (%d)", e.Kind, back.Kind, back.Message, back.HTTPStatus())
		}
	}
	// A status from elsewhere (no detail of ours) still maps by its code.
	if got := FromGRPCStatus(status.New(codes.InvalidArgument, "x")); got.Kind != KindInvalid {
		t.Errorf("got %s", got.Kind)
	}
}
