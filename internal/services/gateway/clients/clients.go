// Package clients dials every backend service the gateway talks to, once, at
// startup.
package clients

import (
	"context"

	"google.golang.org/grpc"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	transactionv1 "github.com/koobam/wuzz-backend/gen/wuzz/transaction/v1"
	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/grpcx"
)

type Clients struct {
	User        userv1.UserServiceClient
	Catalog     catalogv1.CatalogServiceClient
	Transaction transactionv1.TransactionServiceClient

	conns []*grpc.ClientConn
}

func Dial(ctx context.Context, cfg *config.Config) (*Clients, error) {
	userConn, err := grpcx.Dial(cfg.Upstreams.User)
	if err != nil {
		return nil, err
	}

	catalogConn, err := grpcx.Dial(cfg.Upstreams.Catalog)
	if err != nil {
		return nil, err
	}
	transactionConn, err := grpcx.Dial(cfg.Upstreams.Transaction)
	if err != nil {
		return nil, err
	}

	return &Clients{
		User:        userv1.NewUserServiceClient(userConn),
		Catalog:     catalogv1.NewCatalogServiceClient(catalogConn),
		Transaction: transactionv1.NewTransactionServiceClient(transactionConn),
		conns:       []*grpc.ClientConn{userConn, catalogConn, transactionConn},
	}, nil
}

func (c *Clients) Close() error {
	for _, conn := range c.conns {
		_ = conn.Close()
	}
	return nil
}
