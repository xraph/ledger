package id_test

import (
	"strings"
	"testing"

	"github.com/xraph/ledger/id"
)

func TestConstructors(t *testing.T) {
	tests := []struct {
		name   string
		newFn  func() id.ID
		prefix string
	}{
		{"PlanID", id.NewPlanID, "plan_"},
		{"FeatureID", id.NewFeatureID, "feat_"},
		{"PriceID", id.NewPriceID, "price_"},
		{"SubscriptionID", id.NewSubscriptionID, "sub_"},
		{"UsageEventID", id.NewUsageEventID, "uevt_"},
		{"EntitlementID", id.NewEntitlementID, "ent_"},
		{"InvoiceID", id.NewInvoiceID, "inv_"},
		{"LineItemID", id.NewLineItemID, "li_"},
		{"CouponID", id.NewCouponID, "cpn_"},
		{"PaymentID", id.NewPaymentID, "pay_"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.newFn().String()
			if !strings.HasPrefix(got, tt.prefix) {
				t.Errorf("expected prefix %q, got %q", tt.prefix, got)
			}
		})
	}
}

func TestNew(t *testing.T) {
	i := id.New(id.PrefixPlan)
	if i.IsNil() {
		t.Fatal("expected non-nil ID")
	}
	if i.Prefix() != id.PrefixPlan {
		t.Errorf("expected prefix %q, got %q", id.PrefixPlan, i.Prefix())
	}
}

func TestParseRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		newFn   func() id.ID
		parseFn func(string) (id.ID, error)
	}{
		{"PlanID", id.NewPlanID, id.ParsePlanID},
		{"FeatureID", id.NewFeatureID, id.ParseFeatureID},
		{"PriceID", id.NewPriceID, id.ParsePriceID},
		{"SubscriptionID", id.NewSubscriptionID, id.ParseSubscriptionID},
		{"UsageEventID", id.NewUsageEventID, id.ParseUsageEventID},
		{"EntitlementID", id.NewEntitlementID, id.ParseEntitlementID},
		{"InvoiceID", id.NewInvoiceID, id.ParseInvoiceID},
		{"LineItemID", id.NewLineItemID, id.ParseLineItemID},
		{"CouponID", id.NewCouponID, id.ParseCouponID},
		{"PaymentID", id.NewPaymentID, id.ParsePaymentID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := tt.newFn()
			parsed, err := tt.parseFn(original.String())
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if parsed.String() != original.String() {
				t.Errorf("round-trip mismatch: %q != %q", parsed.String(), original.String())
			}
		})
	}
}

func TestCrossTypeRejection(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		parseFn func(string) (id.ID, error)
	}{
		{"ParsePlanID rejects feat_", id.NewFeatureID().String(), id.ParsePlanID},
		{"ParseFeatureID rejects price_", id.NewPriceID().String(), id.ParseFeatureID},
		{"ParsePriceID rejects sub_", id.NewSubscriptionID().String(), id.ParsePriceID},
		{"ParseSubscriptionID rejects uevt_", id.NewUsageEventID().String(), id.ParseSubscriptionID},
		{"ParseUsageEventID rejects ent_", id.NewEntitlementID().String(), id.ParseUsageEventID},
		{"ParseEntitlementID rejects inv_", id.NewInvoiceID().String(), id.ParseEntitlementID},
		{"ParseInvoiceID rejects li_", id.NewLineItemID().String(), id.ParseInvoiceID},
		{"ParseLineItemID rejects cpn_", id.NewCouponID().String(), id.ParseLineItemID},
		{"ParseCouponID rejects pay_", id.NewPaymentID().String(), id.ParseCouponID},
		{"ParsePaymentID rejects plan_", id.NewPlanID().String(), id.ParsePaymentID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.parseFn(tt.input)
			if err == nil {
				t.Errorf("expected error for cross-type parse of %q, got nil", tt.input)
			}
		})
	}
}

func TestParseAny(t *testing.T) {
	ids := []id.ID{
		id.NewPlanID(),
		id.NewFeatureID(),
		id.NewPriceID(),
		id.NewSubscriptionID(),
		id.NewUsageEventID(),
		id.NewEntitlementID(),
		id.NewInvoiceID(),
		id.NewLineItemID(),
		id.NewCouponID(),
		id.NewPaymentID(),
	}

	for _, i := range ids {
		t.Run(i.String(), func(t *testing.T) {
			parsed, err := id.ParseAny(i.String())
			if err != nil {
				t.Fatalf("ParseAny(%q) failed: %v", i.String(), err)
			}
			if parsed.String() != i.String() {
				t.Errorf("round-trip mismatch: %q != %q", parsed.String(), i.String())
			}
		})
	}
}

func TestParseWithPrefix(t *testing.T) {
	i := id.NewPlanID()
	parsed, err := id.ParseWithPrefix(i.String(), id.PrefixPlan)
	if err != nil {
		t.Fatalf("ParseWithPrefix failed: %v", err)
	}
	if parsed.String() != i.String() {
		t.Errorf("mismatch: %q != %q", parsed.String(), i.String())
	}

	_, err = id.ParseWithPrefix(i.String(), id.PrefixFeature)
	if err == nil {
		t.Error("expected error for wrong prefix")
	}
}

func TestParseEmpty(t *testing.T) {
	_, err := id.Parse("")
	if err == nil {
		t.Error("expected error for empty string")
	}
}

func TestNilID(t *testing.T) {
	var i id.ID
	if !i.IsNil() {
		t.Error("zero-value ID should be nil")
	}
	if i.String() != "" {
		t.Errorf("expected empty string, got %q", i.String())
	}
	if i.Prefix() != "" {
		t.Errorf("expected empty prefix, got %q", i.Prefix())
	}
}

func TestMarshalUnmarshalText(t *testing.T) {
	original := id.NewPlanID()
	data, err := original.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText failed: %v", err)
	}

	var restored id.ID
	if unmarshalErr := restored.UnmarshalText(data); unmarshalErr != nil {
		t.Fatalf("UnmarshalText failed: %v", unmarshalErr)
	}
	if restored.String() != original.String() {
		t.Errorf("mismatch: %q != %q", restored.String(), original.String())
	}

	// Nil round-trip.
	var nilID id.ID
	data, err = nilID.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText(nil) failed: %v", err)
	}
	var restored2 id.ID
	if err := restored2.UnmarshalText(data); err != nil {
		t.Fatalf("UnmarshalText(nil) failed: %v", err)
	}
	if !restored2.IsNil() {
		t.Error("expected nil after round-trip of nil ID")
	}
}

func TestValueScan(t *testing.T) {
	original := id.NewSubscriptionID()
	val, err := original.Value()
	if err != nil {
		t.Fatalf("Value failed: %v", err)
	}

	var scanned id.ID
	if scanErr := scanned.Scan(val); scanErr != nil {
		t.Fatalf("Scan failed: %v", scanErr)
	}
	if scanned.String() != original.String() {
		t.Errorf("mismatch: %q != %q", scanned.String(), original.String())
	}

	// Nil round-trip.
	var nilID id.ID
	val, err = nilID.Value()
	if err != nil {
		t.Fatalf("Value(nil) failed: %v", err)
	}
	if val != nil {
		t.Errorf("expected nil value for nil ID, got %v", val)
	}

	var scanned2 id.ID
	if err := scanned2.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) failed: %v", err)
	}
	if !scanned2.IsNil() {
		t.Error("expected nil after scan of nil")
	}
}

func TestUniqueness(t *testing.T) {
	a := id.NewPlanID()
	b := id.NewPlanID()
	if a.String() == b.String() {
		t.Errorf("two consecutive NewPlanID() calls returned the same ID: %q", a.String())
	}
}
