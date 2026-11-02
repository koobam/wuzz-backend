// Package clients dials every backend service the gateway talks to, once, at
// startup.
package clients

import (
	"context"

	"google.golang.org/grpc"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	mediav1 "github.com/koobam/wuzz-backend/gen/wuzz/media/v1"
	transactionv1 "github.com/koobam/wuzz-backend/gen/wuzz/transaction/v1"
	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/grpcx"
)

// messageOverhead is room for the fields around a picture's bytes in one gRPC message.
const messageOverhead = 64 << 10

type Clients struct {
	User        userv1.UserServiceClient
	Catalog     catalogv1.CatalogServiceClient
	Transaction transactionv1.TransactionServiceClient
	Media       mediav1.MediaServiceClient

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
	// A picture travels in one message, so the send limit is raised to the upload limit (the media service raises its receive
	// limit to match).
	mediaConn, err := grpcx.Dial(cfg.Upstreams.Media, grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(int(cfg.Media.MaxUploadBytes)+messageOverhead)))
	if err != nil {
		return nil, err
	}

	return &Clients{
		User:        userv1.NewUserServiceClient(userConn),
		Catalog:     catalogv1.NewCatalogServiceClient(catalogConn),
		Transaction: transactionv1.NewTransactionServiceClient(transactionConn),
		Media:       mediav1.NewMediaServiceClient(mediaConn),
		conns:       []*grpc.ClientConn{userConn, catalogConn, transactionConn, mediaConn},
	}, nil
}

func (c *Clients) Close() error {
	for _, conn := range c.conns {
		_ = conn.Close()
	}
	return nil
}
