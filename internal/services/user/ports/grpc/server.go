// Package grpc is the user service's gRPC transport: it converts proto
// requests into app commands/queries and app results back into proto
// responses. No business rule lives here.
package grpc

import (
	"google.golang.org/grpc"

	userv1 "github.com/koobam/wuzz-backend/gen/wuzz/user/v1"
	"github.com/koobam/wuzz-backend/internal/services/user/app"
)

type Server struct {
	userv1.UnimplementedUserServiceServer
	app *app.App
}

func Register(registrar grpc.ServiceRegistrar, application *app.App) {
	userv1.RegisterUserServiceServer(registrar, &Server{app: application})
}
