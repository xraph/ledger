package stripe

import (
	"github.com/xraph/ledger/plugin"
	"github.com/xraph/ledger/provider"
)

// stripePlugin registers a Provider with Ledger as its payment provider.
type stripePlugin struct {
	p *Provider
}

// NewPlugin wraps p so it can be passed to ledger.WithPlugin. It panics if
// p is nil: build p with New and check its error first.
func NewPlugin(p *Provider) plugin.PaymentProviderPlugin {
	if p == nil {
		panic("stripe: NewPlugin called with a nil Provider; build one with New and check its error")
	}
	return &stripePlugin{p: p}
}

// Name returns "stripe".
func (s *stripePlugin) Name() string { return providerName }

// Provider returns the wrapped Stripe provider.
func (s *stripePlugin) Provider() provider.Provider { return s.p }
