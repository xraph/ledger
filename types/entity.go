// Package types provides common types used across Ledger.
package types

import "time"

// Entity is the base type for all Ledger entities with timestamps.
// Embed this in your domain types to get automatic timestamp handling.
type Entity struct {
	CreatedAt time.Time `json:"created_at" bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt time.Time `json:"updated_at" bun:"updated_at,notnull,default:current_timestamp"`
}

// NewEntity creates a new Entity with current timestamps.
func NewEntity() Entity {
	now := time.Now().UTC()
	return Entity{
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// Touch updates the UpdatedAt timestamp to now.
func (e *Entity) Touch() {
	e.UpdatedAt = time.Now().UTC()
}

// Age returns how long ago the entity was created.
func (e Entity) Age() time.Duration {
	return time.Since(e.CreatedAt)
}

// LastModified returns how long ago the entity was last updated.
func (e Entity) LastModified() time.Duration {
	return time.Since(e.UpdatedAt)
}

// IsNew returns true if the entity was created within the last minute.
func (e Entity) IsNew() bool {
	return e.Age() < time.Minute
}

// IsStale returns true if the entity hasn't been updated in the specified duration.
func (e Entity) IsStale(staleDuration time.Duration) bool {
	return e.LastModified() > staleDuration
}
