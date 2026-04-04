package qdrantkit

import (
	"encoding/json"
	"fmt"
)

// --------------------------------------------------------------------------
// Filter
// --------------------------------------------------------------------------

// Filter is a Qdrant filter with boolean clauses. Use Must, Should, or
// MustNot constructors, or build the struct directly for complex filters.
type Filter struct {
	Must    []Condition `json:"must,omitempty"`
	Should  []Condition `json:"should,omitempty"`
	MustNot []Condition `json:"must_not,omitempty"`
}

// Must returns a filter where all conditions must match.
func Must(conds ...Condition) *Filter {
	return &Filter{Must: conds}
}

// Should returns a filter where at least one condition must match.
func Should(conds ...Condition) *Filter {
	return &Filter{Should: conds}
}

// MustNot returns a filter where no conditions may match.
func MustNot(conds ...Condition) *Filter {
	return &Filter{MustNot: conds}
}

// --------------------------------------------------------------------------
// Condition
// --------------------------------------------------------------------------

type condKind int

const (
	condMatch condKind = iota
	condRange
	condHasID
)

// Condition is a single filter predicate. Build with Match, Range, or HasID.
type Condition struct {
	kind  condKind
	key   string
	value any
	rng   rangeSpec
	ids   []string
}

type rangeSpec struct {
	GT  *float64
	GTE *float64
	LT  *float64
	LTE *float64
}

// MarshalJSON produces the Qdrant JSON representation of the condition.
func (c Condition) MarshalJSON() ([]byte, error) {
	switch c.kind {
	case condMatch:
		return json.Marshal(map[string]any{
			"key":   c.key,
			"match": map[string]any{"value": c.value},
		})
	case condRange:
		r := make(map[string]any)
		if c.rng.GT != nil {
			r["gt"] = *c.rng.GT
		}
		if c.rng.GTE != nil {
			r["gte"] = *c.rng.GTE
		}
		if c.rng.LT != nil {
			r["lt"] = *c.rng.LT
		}
		if c.rng.LTE != nil {
			r["lte"] = *c.rng.LTE
		}
		return json.Marshal(map[string]any{
			"key":   c.key,
			"range": r,
		})
	case condHasID:
		return json.Marshal(map[string]any{
			"has_id": c.ids,
		})
	default:
		return nil, fmt.Errorf("qdrantkit: unknown condition kind %d", c.kind)
	}
}

// --------------------------------------------------------------------------
// Condition builders
// --------------------------------------------------------------------------

// Match creates an exact-match condition on a payload field.
func Match(key string, value any) Condition {
	return Condition{kind: condMatch, key: key, value: value}
}

// RangeOpt configures a range condition bound.
type RangeOpt func(*rangeSpec)

// Lt sets an exclusive upper bound.
func Lt(v float64) RangeOpt { return func(r *rangeSpec) { r.LT = &v } }

// Lte sets an inclusive upper bound.
func Lte(v float64) RangeOpt { return func(r *rangeSpec) { r.LTE = &v } }

// Gt sets an exclusive lower bound.
func Gt(v float64) RangeOpt { return func(r *rangeSpec) { r.GT = &v } }

// Gte sets an inclusive lower bound.
func Gte(v float64) RangeOpt { return func(r *rangeSpec) { r.GTE = &v } }

// Range creates a numeric range condition on a payload field.
func Range(key string, opts ...RangeOpt) Condition {
	var spec rangeSpec
	for _, o := range opts {
		o(&spec)
	}
	return Condition{kind: condRange, key: key, rng: spec}
}

// HasID creates a condition that matches specific point IDs.
func HasID(ids ...string) Condition {
	return Condition{kind: condHasID, ids: ids}
}
