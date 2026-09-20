// Package app wires the user service's use cases together. cmd/user/main.go
// constructs the adapters (Postgres, Redis, Twilio) and hands them here;
// nothing below this package knows those adapters exist.
package app

import (
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/services/user/app/command"
	"github.com/koobam/wuzz-backend/internal/services/user/app/query"
)

type Commands struct {
	Register  *command.RegisterHandler
	VerifyOtp *command.VerifyOtpHandler
	Refresh   *command.RefreshHandler
	Logout    *command.LogoutHandler
}

type Queries struct {
	GetUser         *query.GetUserHandler
	CheckUsername   *query.CheckUsernameHandler
	SuggestUsername *query.SuggestUsernameHandler
}

type App struct {
	Commands Commands
	Queries  Queries
}

func New(
	users command.Users,
	pending command.PendingRegistrations,
	sessions command.Sessions,
	verifications command.Verifications,
	signer *authn.Signer,
	accessTTL time.Duration,
	codeTTL time.Duration,
) *App {
	return &App{
		Commands: Commands{
			Register:  command.NewRegisterHandler(users, pending, verifications, codeTTL),
			VerifyOtp: command.NewVerifyOtpHandler(users, pending, verifications, sessions, signer, accessTTL),
			Refresh:   command.NewRefreshHandler(users, sessions, signer, accessTTL),
			Logout:    command.NewLogoutHandler(sessions),
		},
		Queries: Queries{
			GetUser:         query.NewGetUserHandler(queryUsers{users}),
			CheckUsername:   query.NewCheckUsernameHandler(queryUsers{users}),
			SuggestUsername: query.NewSuggestUsernameHandler(queryUsers{users}),
		},
	}
}

// queryUsers narrows command.Users down to query.Users, so the same Postgres
// adapter serves both sides without either package importing the other's
// port interface.
type queryUsers struct{ command.Users }
