// Package grpc serves media's gRPC API.
package grpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	mediav1 "github.com/koobam/wuzz-backend/gen/wuzz/media/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/services/media/app"
	"github.com/koobam/wuzz-backend/internal/services/media/domain"
)

type Server struct {
	mediav1.UnimplementedMediaServiceServer
	app *app.App
}

func Register(registrar grpc.ServiceRegistrar, application *app.App) {
	mediav1.RegisterMediaServiceServer(registrar, &Server{app: application})
}

// requireAdmin guards the library's writes and its listing; reading a picture by id needs only a signed-in caller (the gateway
// resolves ids on behalf of administrators, and the addresses themselves are public).
func requireAdmin(ctx context.Context) (authn.Identity, error) {
	id, err := authn.Require(ctx)
	if err != nil {
		return id, err
	}
	if !id.HasRole("admin") {
		return id, apperr.Forbidden("Administrator access is required")
	}
	return id, nil
}

func toProto(m domain.Media) *mediav1.Media {
	return &mediav1.Media{Id: m.ID, Key: m.Key, Url: m.URL, Folder: m.Folder, FileName: m.FileName, ContentType: m.ContentType,
		Size: m.Size, Width: int32(m.Width), Height: int32(m.Height), UploadedBy: m.UploadedBy, CreatedAt: timestamppb.New(m.CreatedAt)}
}

func (s *Server) Upload(ctx context.Context, req *mediav1.UploadRequest) (*mediav1.UploadResponse, error) {
	who, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.app.Upload(ctx, app.Upload{Data: req.GetData(), FileName: req.GetFileName(), Folder: req.GetFolder(), UploadedBy: who.UserID})
	if err != nil {
		return nil, err
	}
	return &mediav1.UploadResponse{Media: toProto(m)}, nil
}

func (s *Server) GetMedia(ctx context.Context, req *mediav1.GetMediaRequest) (*mediav1.GetMediaResponse, error) {
	if _, err := authn.Require(ctx); err != nil {
		return nil, err
	}
	m, err := s.app.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &mediav1.GetMediaResponse{Media: toProto(m)}, nil
}

func (s *Server) BatchGetMedia(ctx context.Context, req *mediav1.BatchGetMediaRequest) (*mediav1.BatchGetMediaResponse, error) {
	if _, err := authn.Require(ctx); err != nil {
		return nil, err
	}
	list, err := s.app.BatchGet(ctx, req.GetIds())
	if err != nil {
		return nil, err
	}
	out := &mediav1.BatchGetMediaResponse{}
	for _, m := range list {
		out.Media = append(out.Media, toProto(m))
	}
	return out, nil
}

func (s *Server) ListMedia(ctx context.Context, req *mediav1.ListMediaRequest) (*mediav1.ListMediaResponse, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	list, total, err := s.app.List(ctx, domain.ListQuery{Folder: req.GetFolder(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset())})
	if err != nil {
		return nil, err
	}
	out := &mediav1.ListMediaResponse{Total: total}
	for _, m := range list {
		out.Media = append(out.Media, toProto(m))
	}
	return out, nil
}

func (s *Server) DeleteMedia(ctx context.Context, req *mediav1.DeleteMediaRequest) (*mediav1.DeleteMediaResponse, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if err := s.app.Delete(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &mediav1.DeleteMediaResponse{}, nil
}
