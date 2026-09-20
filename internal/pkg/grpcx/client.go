package grpcx

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
)

// Dial opens a gRPC client connection to an internal service address.
// Plaintext: TLS terminates at the edge (the gateway), not between internal
// services running inside the same private network.
func Dial(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(authn.UnaryClientInterceptor()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`),
	)
}
