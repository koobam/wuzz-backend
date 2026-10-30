package domain

import "context"

// What an order's status change does to the stock.
const (
	StockConsume = "consume"
	StockReverse = "reverse"
)

// StockActionFor says what moving an order from one status to another does to the stock: an order that becomes 'processing' takes
// its materials out; one cancelled while 'processing' puts them back. Anything else leaves the stock alone, and returns "".
func StockActionFor(from, to string) string {
	switch {
	case to == StatusProcessing:
		return StockConsume
	case to == StatusCancelled && from == StatusProcessing:
		return StockReverse
	}
	return ""
}

// StatusEffect is work that has to happen together with a status change. The store runs it inside the transaction that holds the
// order's row, once the change is known to be allowed and before it commits: if it fails, the status does not change.
type StatusEffect func(ctx context.Context, before, after Order) error
