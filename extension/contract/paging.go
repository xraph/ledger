package contract

import (
	"github.com/xraph/ledger/id"
)

const (
	defaultPageLimit = 50
	maxPageLimit     = 200
)

// PageInput is the paging half of every list request.
type PageInput struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// window normalises the request: a missing or negative limit becomes the
// default, a limit above the maximum is clamped, a negative offset is zero.
func (p PageInput) window() (limit, offset int) {
	limit = p.Limit
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	offset = p.Offset
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// Page is the shape of every list response.
type Page[T any] struct {
	Items   []T  `json:"items"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"has_more"`
}

// pageFrom builds a page from rows fetched with limit+1, so one extra row means
// there is more. Items is never nil, so the wire carries [] rather than null.
func pageFrom[T any](fetched []T, limit, offset int) Page[T] {
	items := make([]T, 0, limit)
	hasMore := len(fetched) > limit
	for i := 0; i < len(fetched) && i < limit; i++ {
		items = append(items, fetched[i])
	}
	return Page[T]{Items: items, Limit: limit, Offset: offset, HasMore: hasMore}
}

// parseID parses a required id field, answering BAD_REQUEST with the field's
// name when it is missing, malformed, or carries another entity's prefix.
func parseID(field, raw string, parse func(string) (id.ID, error)) (id.ID, error) {
	if raw == "" {
		return id.Nil, badRequest("%s is required", field)
	}
	parsed, err := parse(raw)
	if err != nil {
		return id.Nil, badRequest("%s is not a valid id: %s", field, raw)
	}
	return parsed, nil
}
