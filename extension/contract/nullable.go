package contract

import "encoding/json"

// Nullable is an update field that can be left alone, cleared, or set. A
// pointer cannot do this: JSON null and an absent key both decode to nil. A
// non-pointer type implementing json.Unmarshaler is called for null, so the
// three cases stay distinct.
type Nullable[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(b) == "null" {
		n.Null = true
		return nil
	}
	return json.Unmarshal(b, &n.Value)
}

// apply writes the field's effect onto an optional value.
func (n Nullable[T]) apply(current *T) *T {
	switch {
	case !n.Set:
		return current
	case n.Null:
		return nil
	default:
		v := n.Value
		return &v
	}
}
