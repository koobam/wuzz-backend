// Package app wires transaction's use cases: a write side in app/command and a read side in app/query, one handler per
// use case. Nothing here holds a rule of its own; who may touch which outlet's orders lives in domain.Caller and in the handlers.
//
// Handlers state the few repository methods they need as small ports (command.Orders, query.Orders). Store is their union:
// the whole of what the Postgres adapter has to provide, so a missing method fails to compile here.
package app

import (
	"github.com/koobam/wuzz-backend/internal/services/transaction/app/command"
	"github.com/koobam/wuzz-backend/internal/services/transaction/app/query"
)

// Store is every repository method the handlers use, implemented by adapters/postgres.
type Store interface {
	command.Orders
	query.Orders
}

type Commands struct {
	CreateOrder    *command.CreateOrderHandler
	SetOrderStatus *command.SetOrderStatusHandler
}

type Queries struct {
	GetOrder        *query.GetOrderHandler
	ListOrders      *query.ListOrdersHandler
	SummarizeOrders *query.SummarizeOrdersHandler
	OrderStats      *query.OrderStatsHandler
}

type App struct {
	Commands Commands
	Queries  Queries
}

func New(store Store, stock command.Stock) *App {
	return &App{
		Commands: Commands{
			CreateOrder:    command.NewCreateOrderHandler(store),
			SetOrderStatus: command.NewSetOrderStatusHandler(store, stock),
		},
		Queries: Queries{
			GetOrder:        query.NewGetOrderHandler(store),
			ListOrders:      query.NewListOrdersHandler(store),
			SummarizeOrders: query.NewSummarizeOrdersHandler(store),
			OrderStats:      query.NewOrderStatsHandler(store),
		},
	}
}
