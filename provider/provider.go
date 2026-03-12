// Package provider defines the contract that payment provider plugins
// (Stripe, Braintree, etc.) must implement. Payment methods are always
// fetched live from the provider and never stored locally.
package provider

import (
	"context"
	"time"

	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/subscription"
)

// Provider is the interface that payment provider plugins must implement.
// It covers bidirectional sync (push local → provider, pull provider → local),
// live payment-method listing, and webhook handling.
type Provider interface {
	// Name returns the provider identifier (e.g. "stripe", "braintree").
	Name() string

	// ── Payment Methods (read-only, fetched live from provider) ──

	ListPaymentMethods(ctx context.Context, tenantID string) ([]PaymentMethod, error)

	// ── Sync: push local entities to provider ──

	SyncPlan(ctx context.Context, p *plan.Plan) (providerID string, err error)
	SyncFeature(ctx context.Context, f *feature.Feature) (providerID string, err error)
	SyncSubscription(ctx context.Context, s *subscription.Subscription) (providerID string, err error)
	SyncInvoice(ctx context.Context, inv *invoice.Invoice) (providerID string, err error)

	// ── Import: pull from provider to local (data recovery) ──

	ImportPlan(ctx context.Context, providerID string) (*plan.Plan, error)
	ImportFeature(ctx context.Context, providerID string) (*feature.Feature, error)
	ImportSubscription(ctx context.Context, providerID string) (*subscription.Subscription, error)
	ImportInvoice(ctx context.Context, providerID string) (*invoice.Invoice, error)

	// ── Webhooks ──

	HandleWebhook(ctx context.Context, payload []byte) (*WebhookResult, error)
}

// PaymentMethod is a read-only DTO representing a payment method on the
// provider side. It is never persisted locally.
type PaymentMethod struct {
	ID           string `json:"id"`
	Type         string `json:"type"`          // "card", "bank_account", etc.
	Last4        string `json:"last4"`         // last 4 digits
	Brand        string `json:"brand"`         // "visa", "mastercard", etc.
	ExpiryMonth  int    `json:"expiry_month"`
	ExpiryYear   int    `json:"expiry_year"`
	IsDefault    bool   `json:"is_default"`
	ProviderName string `json:"provider_name"` // e.g. "stripe"
	ProviderID   string `json:"provider_id"`   // provider's own ID for this method
}

// WebhookResult describes the outcome of processing a single webhook event.
type WebhookResult struct {
	EventType  string    `json:"event_type"`
	ProviderID string    `json:"provider_id"`
	EntityType string    `json:"entity_type"` // "plan", "subscription", "invoice", etc.
	EntityID   string    `json:"entity_id"`
	Handled    bool      `json:"handled"`
	Error      string    `json:"error,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// SyncResult describes the outcome of a push or pull sync operation.
type SyncResult struct {
	ProviderName string `json:"provider_name"`
	ProviderID   string `json:"provider_id"`
	EntityType   string `json:"entity_type"` // "plan", "feature", "subscription", "invoice"
	EntityID     string `json:"entity_id"`
	Direction    string `json:"direction"` // "push" or "pull"
	Success      bool   `json:"success"`
	Error        string `json:"error,omitempty"`
}
