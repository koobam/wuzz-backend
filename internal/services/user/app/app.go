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

	SendLoginCode *command.SendLoginCodeHandler
	// DevLogin is nil outside development; see cmd/user/main.go.
	DevLogin      *command.DevLoginHandler
	SetUserStatus *command.SetUserStatusHandler
}

type Queries struct {
	GetUser         *query.GetUserHandler
	MemberSummary   *query.GetMemberSummaryHandler
	CheckUsername   *query.CheckUsernameHandler
	SuggestUsername *query.SuggestUsernameHandler
	ListUsers       *command.ListUsersHandler
}

type App struct {
	Commands Commands
	Queries  Queries
}

func New(
	users command.Users,
	members query.Members,
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

			SendLoginCode: command.NewSendLoginCodeHandler(users, verifications, codeTTL),
			SetUserStatus: command.NewSetUserStatusHandler(users),
		},
		Queries: Queries{
			GetUser:         query.NewGetUserHandler(queryUsers{users}),
			MemberSummary:   query.NewGetMemberSummaryHandler(members),
			CheckUsername:   query.NewCheckUsernameHandler(queryUsers{users}),
			SuggestUsername: query.NewSuggestUsernameHandler(queryUsers{users}),
			ListUsers:       command.NewListUsersHandler(users),
		},
	}
}

// queryUsers narrows command.Users down to query.Users, so the same Postgres
// adapter serves both sides without either package importing the other's
// port interface.
type queryUsers struct{ command.Users }
