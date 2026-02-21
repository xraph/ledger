package invoice

import (
	"time"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

type Status string

const (
	StatusDraft   Status = "draft"
	StatusPending Status = "pending"
	StatusPaid    Status = "paid"
	StatusPastDue Status = "past_due"
	StatusVoided  Status = "voided"
)

type Invoice struct {
	types.Entity
	ID             id.InvoiceID      `json:"id"`
	TenantID       string            `json:"tenant_id"`
	SubscriptionID id.SubscriptionID `json:"subscription_id"`
	Status         Status            `json:"status"`
	Currency       string            `json:"currency"`
	Subtotal       types.Money       `json:"subtotal"`
	TaxAmount      types.Money       `json:"tax_amount"`
	DiscountAmount types.Money       `json:"discount_amount"`
	Total          types.Money       `json:"total"`
	LineItems      []LineItem        `json:"line_items"`
	PeriodStart    time.Time         `json:"period_start"`
	PeriodEnd      time.Time         `json:"period_end"`
	DueDate        *time.Time        `json:"due_date,omitempty"`
	PaidAt         *time.Time        `json:"paid_at,omitempty"`
	VoidedAt       *time.Time        `json:"voided_at,omitempty"`
	VoidReason     string            `json:"void_reason,omitempty"`
	PaymentRef     string            `json:"payment_ref,omitempty"`
	ProviderID     string            `json:"provider_id,omitempty"`
	AppID          string            `json:"app_id"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type LineItem struct {
	ID          id.LineItemID     `json:"id"`
	InvoiceID   id.InvoiceID      `json:"invoice_id"`
	FeatureKey  string            `json:"feature_key,omitempty"`
	Description string            `json:"description"`
	Quantity    int64             `json:"quantity"`
	UnitAmount  types.Money       `json:"unit_amount"`
	Amount      types.Money       `json:"amount"`
	Type        LineItemType      `json:"type"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type LineItemType string

const (
	LineItemBase     LineItemType = "base"
	LineItemUsage    LineItemType = "usage"
	LineItemOverage  LineItemType = "overage"
	LineItemSeat     LineItemType = "seat"
	LineItemDiscount LineItemType = "discount"
	LineItemTax      LineItemType = "tax"
)
