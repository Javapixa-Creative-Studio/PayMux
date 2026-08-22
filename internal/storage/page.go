package storage

// Pagination defaults shared by every list endpoint.
const (
	DefaultPageLimit = 25
	MaxPageLimit     = 100
)

// Page describes a keyset page request.
//
// PayMux paginates on identifier rather than offset: identifiers are ULIDs, so
// ordering by id descending is ordering by creation time, and a page stays
// stable even as new rows arrive at the head of the list.
type Page struct {
	Limit int
	// StartingAfter returns the page following this identifier.
	StartingAfter string
	// EndingBefore returns the page preceding this identifier.
	EndingBefore string
}

// Normalize clamps the limit into range and returns the value to use.
func (p Page) Normalize() Page {
	if p.Limit <= 0 {
		p.Limit = DefaultPageLimit
	}
	if p.Limit > MaxPageLimit {
		p.Limit = MaxPageLimit
	}
	return p
}

// FetchLimit is the number of rows to read: one extra row reveals whether a
// further page exists without a second count query.
func (p Page) FetchLimit() int { return p.Normalize().Limit + 1 }

// List is a page of results plus the cursor state a client needs to continue.
type List[T any] struct {
	Items   []T    `json:"data"`
	HasMore bool   `json:"has_more"`
	Limit   int    `json:"limit"`
	NextURL string `json:"-"`
}

// NewList trims the sentinel row fetched by FetchLimit and reports whether
// more results remain.
func NewList[T any](items []T, p Page) List[T] {
	p = p.Normalize()
	hasMore := len(items) > p.Limit
	if hasMore {
		items = items[:p.Limit]
	}
	if items == nil {
		items = []T{}
	}
	return List[T]{Items: items, HasMore: hasMore, Limit: p.Limit}
}
