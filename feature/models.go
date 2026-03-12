// Package feature provides standalone, reusable feature definitions for the
// Ledger billing engine. Features can be global (AppID="") or scoped to a
// specific application, and multiple plans can reference the same catalog
// feature via plan.Feature.CatalogID.
package feature

import (
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

// Status represents the lifecycle state of a catalog feature.
type Status string

const (
	StatusActive   Status = "active"
	StatusArchived Status = "archived"
	StatusDraft    Status = "draft"
)

// FeatureType identifies how the feature is measured and enforced.
// Values mirror plan.FeatureType to ensure seamless mapping without
// circular imports (feature must not import plan).
type FeatureType string

const (
	FeatureMetered FeatureType = "metered"
	FeatureBoolean FeatureType = "boolean"
	FeatureSeat    FeatureType = "seat"
)

// Period defines the reset interval for usage tracking.
// Values mirror plan.Period for the same reason as FeatureType.
type Period string

const (
	PeriodMonthly Period = "monthly"
	PeriodYearly  Period = "yearly"
	PeriodNone    Period = "none"
)

// Feature is a standalone, reusable feature definition in the catalog.
// Features can be global (AppID="") or scoped to a specific app.
// Multiple plans can reference the same catalog feature via plan.Feature.CatalogID.
type Feature struct {
	types.Entity
	ID           id.FeatureID      `json:"id"`
	Key          string            `json:"key"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Type         FeatureType       `json:"type"`
	DefaultLimit int64             `json:"default_limit"`
	Period       Period            `json:"period"`
	SoftLimit    bool              `json:"soft_limit"`
	Status       Status            `json:"status"`
	AppID        string            `json:"app_id"`
	ProviderID   string            `json:"provider_id,omitempty"`
	ProviderName string            `json:"provider_name,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// ListOpts configures feature listing queries.
type ListOpts struct {
	Status Status
	Limit  int
	Offset int
}
