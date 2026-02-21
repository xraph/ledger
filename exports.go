package ledger

import "github.com/xraph/ledger/types"

// Re-export common types for convenience so users don't have to import types package.

// Money is re-exported from types package.
type Money = types.Money

// Entity is re-exported from types package.
type Entity = types.Entity

// Re-export Money constructors
var (
	USD  = types.USD
	EUR  = types.EUR
	GBP  = types.GBP
	JPY  = types.JPY
	CAD  = types.CAD
	AUD  = types.AUD
	Zero = types.Zero
	Sum  = types.Sum
)

// Re-export Entity constructor
var NewEntity = types.NewEntity
