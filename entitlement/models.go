package entitlement

type Result struct {
	Allowed   bool   `json:"allowed"`
	Feature   string `json:"feature"`
	Used      int64  `json:"used"`
	Limit     int64  `json:"limit"`
	Remaining int64  `json:"remaining"`
	SoftLimit bool   `json:"soft_limit"`
	Reason    string `json:"reason,omitempty"`
}
