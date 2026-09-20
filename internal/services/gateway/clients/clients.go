// Package clients dials every backend service the gateway talks to, once, at
// startup.
package clients

import (
	"context"

	"google.golang.org/grpc"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/grpcx"
)

type Clients struct {
	User userv1.UserServiceClient

	conns []*grpc.ClientConn
}

func Dial(ctx context.Context, cfg *config.Config) (*Clients, error) {
	userConn, err := grpcx.Dial(cfg.Upstreams.User)
	if err != nil {
		return nil, err
	}

	return &Clients{
		User:  userv1.NewUserServiceClient(userConn),
		conns: []*grpc.ClientConn{userConn},
	}, nil
}

func (c *Clients) Close() error {
	for _, conn := range c.conns {
		_ = conn.Close()
	}
	return nil
}
