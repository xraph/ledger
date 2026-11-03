package stripe

import (
	"github.com/xraph/ledger/plugin"
	"github.com/xraph/ledger/provider"
)

// stripePlugin registers a Provider with Ledger as its payment provider.
type stripePlugin struct {
	p *Provider
}

// NewPlugin wraps p so it can be passed to ledger.WithPlugin.
func NewPlugin(p *Provider) plugin.PaymentProviderPlugin {
	return &stripePlugin{p: p}
}

// Name returns "stripe".
func (s *stripePlugin) Name() string { return providerName }

// Provider returns the wrapped Stripe provider.
func (s *stripePlugin) Provider() provider.Provider { return s.p }
