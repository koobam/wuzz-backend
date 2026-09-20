package authn

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const authorizationKey = "authorization"

// UnaryServerInterceptor verifies the bearer token in metadata and puts the
// identity on the context.
//
// A missing or bad token is not rejected here — it is simply absent from the
// context, and handlers that need a user call Require. That keeps "who may
// call this" a decision of the handler rather than of the transport layer.
func UnaryServerInterceptor(verifier *Verifier) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(withIdentityFromMetadata(ctx, verifier), req)
	}
}

func withIdentityFromMetadata(ctx context.Context, verifier *Verifier) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	values := md.Get(authorizationKey)
	if len(values) == 0 {
		return ctx
	}

	token, found := strings.CutPrefix(values[0], "Bearer ")
	if !found {
		return ctx
	}

	id, err := verifier.Parse(strings.TrimSpace(token))
	if err != nil {
		return ctx
	}
	return WithIdentity(ctx, id)
}

// UnaryClientInterceptor forwards the caller's token downstream, so the
// gateway does not have to thread it through every call signature.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(forwardToken(ctx), method, req, reply, cc, opts...)
	}
}

type tokenKey struct{}

// WithToken stores the raw bearer token for downstream propagation. The
// gateway sets it from the Authorization header it received.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey{}, token)
}

func forwardToken(ctx context.Context) context.Context {
	token, ok := ctx.Value(tokenKey{}).(string)
	if !ok || token == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, authorizationKey, "Bearer "+token)
}
