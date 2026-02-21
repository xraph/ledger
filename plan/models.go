package plan

import (
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusArchived Status = "archived"
	StatusDraft    Status = "draft"
)

type Plan struct {
	types.Entity
	ID          id.PlanID         `json:"id"`
	Name        string            `json:"name"`
	Slug        string            `json:"slug"`
	Description string            `json:"description"`
	Currency    string            `json:"currency"`
	Status      Status            `json:"status"`
	TrialDays   int               `json:"trial_days"`
	Features    []Feature         `json:"features"`
	Pricing     *Pricing          `json:"pricing,omitempty"`
	AppID       string            `json:"app_id"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Feature struct {
	types.Entity
	ID        id.FeatureID      `json:"id"`
	Key       string            `json:"key"`
	Name      string            `json:"name"`
	Type      FeatureType       `json:"type"`
	Limit     int64             `json:"limit"`
	Period    Period            `json:"period"`
	SoftLimit bool              `json:"soft_limit"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type FeatureType string

const (
	FeatureMetered FeatureType = "metered"
	FeatureBoolean FeatureType = "boolean"
	FeatureSeat    FeatureType = "seat"
)

type Period string

const (
	PeriodMonthly Period = "monthly"
	PeriodYearly  Period = "yearly"
	PeriodNone    Period = "none"
)

type Pricing struct {
	types.Entity
	ID            id.PriceID  `json:"id"`
	PlanID        id.PlanID   `json:"plan_id"`
	BaseAmount    types.Money `json:"base_amount"`
	BillingPeriod Period      `json:"billing_period"`
	Tiers         []PriceTier `json:"tiers,omitempty"`
}

type TierType string

const (
	TierGraduated TierType = "graduated"
	TierVolume    TierType = "volume"
	TierFlat      TierType = "flat"
)

type PriceTier struct {
	FeatureKey string      `json:"feature_key"`
	Type       TierType    `json:"type"`
	UpTo       int64       `json:"up_to"`
	UnitAmount types.Money `json:"unit_amount"`
	FlatAmount types.Money `json:"flat_amount"`
	Priority   int         `json:"priority"`
}

func (p *Plan) FindFeature(key string) *Feature {
	for i := range p.Features {
		if p.Features[i].Key == key {
			return &p.Features[i]
		}
	}
	return nil
}

func (p *Plan) Allows(featureKey string, currentUsage int64) bool {
	f := p.FindFeature(featureKey)
	if f == nil {
		return false
	}
	if f.Type == FeatureBoolean {
		return f.Limit > 0
	}
	if f.Limit == -1 {
		return true
	}
	if currentUsage < f.Limit {
		return true
	}
	return f.SoftLimit
}
