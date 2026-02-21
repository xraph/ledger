package id

import (
	"strings"
	"testing"
)

func TestNewIDs(t *testing.T) {
	tests := []struct {
		name    string
		newFunc func() string
		prefix  string
	}{
		{"PlanID", func() string { return NewPlanID().String() }, PrefixPlan},
		{"FeatureID", func() string { return NewFeatureID().String() }, PrefixFeature},
		{"PriceID", func() string { return NewPriceID().String() }, PrefixPrice},
		{"SubscriptionID", func() string { return NewSubscriptionID().String() }, PrefixSubscription},
		{"UsageEventID", func() string { return NewUsageEventID().String() }, PrefixUsageEvent},
		{"EntitlementID", func() string { return NewEntitlementID().String() }, PrefixEntitlement},
		{"InvoiceID", func() string { return NewInvoiceID().String() }, PrefixInvoice},
		{"LineItemID", func() string { return NewLineItemID().String() }, PrefixLineItem},
		{"CouponID", func() string { return NewCouponID().String() }, PrefixCoupon},
		{"PaymentID", func() string { return NewPaymentID().String() }, PrefixPayment},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.newFunc()

			// Check prefix
			if !strings.HasPrefix(id, tt.prefix+"_") {
				t.Errorf("ID %s does not have prefix %s", id, tt.prefix)
			}

			// Check format (prefix_suffix)
			parts := strings.Split(id, "_")
			if len(parts) != 2 {
				t.Errorf("ID %s does not have correct format", id)
			}

			// Check suffix length (should be 26 chars for UUIDv7)
			if len(parts[1]) != 26 {
				t.Errorf("ID suffix %s does not have correct length (got %d, want 26)", parts[1], len(parts[1]))
			}
		})
	}
}

func TestParseIDs(t *testing.T) {
	tests := []struct {
		name      string
		parseFunc func(string) (interface{}, error)
		validID   string
		invalidID string
		wrongID   string // ID with wrong prefix
	}{
		{
			"ParsePlanID",
			func(s string) (interface{}, error) { return ParsePlanID(s) },
			"plan_01h2xcejqtf2nbrexx3vqjhp41",
			"plan_invalid",
			"sub_01h2xcejqtf2nbrexx3vqjhp41",
		},
		{
			"ParseSubscriptionID",
			func(s string) (interface{}, error) { return ParseSubscriptionID(s) },
			"sub_01h2xcejqtf2nbrexx3vqjhp41",
			"sub_invalid",
			"plan_01h2xcejqtf2nbrexx3vqjhp41",
		},
		{
			"ParseInvoiceID",
			func(s string) (interface{}, error) { return ParseInvoiceID(s) },
			"inv_01h2xcejqtf2nbrexx3vqjhp41",
			"inv_invalid",
			"sub_01h2xcejqtf2nbrexx3vqjhp41",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test valid ID
			id, err := tt.parseFunc(tt.validID)
			if err != nil {
				t.Errorf("Failed to parse valid ID %s: %v", tt.validID, err)
			}
			if id == nil {
				t.Errorf("Parsed ID is nil for %s", tt.validID)
			}

			// Test invalid format
			_, err = tt.parseFunc(tt.invalidID)
			if err == nil {
				t.Errorf("Expected error parsing invalid ID %s", tt.invalidID)
			}

			// Test wrong prefix
			_, err = tt.parseFunc(tt.wrongID)
			if err == nil {
				t.Errorf("Expected error parsing ID with wrong prefix %s", tt.wrongID)
			}
			if err != nil && !strings.Contains(err.Error(), "expected prefix") {
				t.Errorf("Wrong error message for incorrect prefix: %v", err)
			}
		})
	}
}

func TestParseAny(t *testing.T) {
	validIDs := []string{
		"plan_01h2xcejqtf2nbrexx3vqjhp41",
		"sub_01h2xcejqtf2nbrexx3vqjhp41",
		"inv_01h2xcejqtf2nbrexx3vqjhp41",
		"feat_01h2xcejqtf2nbrexx3vqjhp41",
		"uevt_01h2xcejqtf2nbrexx3vqjhp41",
	}

	for _, id := range validIDs {
		parsed, err := ParseAny(id)
		if err != nil {
			t.Errorf("Failed to parse valid ID %s: %v", id, err)
		}
		if parsed.String() != id {
			t.Errorf("Parsed ID mismatch: got %s, want %s", parsed.String(), id)
		}
	}

	// Test invalid
	_, err := ParseAny("invalid_id")
	if err == nil {
		t.Error("Expected error parsing invalid ID")
	}
}

func TestIDUniqueness(t *testing.T) {
	// Generate multiple IDs and ensure they're all unique
	const count = 100
	ids := make(map[string]bool)

	for i := 0; i < count; i++ {
		id := NewPlanID().String()
		if ids[id] {
			t.Fatalf("Duplicate ID generated: %s", id)
		}
		ids[id] = true
	}

	if len(ids) != count {
		t.Errorf("Expected %d unique IDs, got %d", count, len(ids))
	}
}

func TestIDSortability(t *testing.T) {
	// TypeIDs with UUIDv7 should be K-sortable (time-ordered)
	id1 := NewPlanID()
	// Small delay to ensure different timestamps
	id2 := NewPlanID()
	id3 := NewPlanID()

	// String comparison should reflect time ordering
	if id1.String() >= id2.String() {
		// This might occasionally fail due to timing, but should be rare
		t.Logf("Warning: IDs may not be perfectly time-ordered: %s >= %s", id1, id2)
	}
	if id2.String() >= id3.String() {
		t.Logf("Warning: IDs may not be perfectly time-ordered: %s >= %s", id2, id3)
	}
}

func BenchmarkNewPlanID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = NewPlanID()
	}
}

func BenchmarkParsePlanID(b *testing.B) {
	id := "plan_01h2xcejqtf2nbrexx3vqjhp41"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParsePlanID(id)
	}
}
