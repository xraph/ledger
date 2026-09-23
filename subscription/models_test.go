package subscription

import (
	"encoding/json"
	"testing"
)

func TestSubscriptionQuantityRoundTrips(t *testing.T) {
	in := Subscription{
		TenantID: "tenant_1",
		Quantity: map[string]int64{"seats": 12, "projects": 3},
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var out Subscription
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if out.Quantity["seats"] != 12 {
		t.Errorf("got seats %d, want 12", out.Quantity["seats"])
	}
	if out.Quantity["projects"] != 3 {
		t.Errorf("got projects %d, want 3", out.Quantity["projects"])
	}
}

func TestSubscriptionQuantityOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(Subscription{TenantID: "tenant_1"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var generic map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if _, present := generic["quantity"]; present {
		t.Error("quantity appears in JSON for a subscription that has none; it should be omitted")
	}
}
