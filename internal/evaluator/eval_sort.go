package evaluator

import (
	"slices"
	"sort"

	"github.com/recolabs/gnata/internal/parser"
)

func evalSort(node *parser.Node, input any, env *Environment) (any, error) {
	items, err := Eval(node.Left, input, env)
	if err != nil {
		return nil, err
	}
	if items == nil {
		return nil, nil
	}

	var arr []any
	wasArray := true
	switch v := items.(type) {
	case []any:
		arr = v
	case *Sequence:
		collapsed := CollapseSequence(v)
		if collapsed == nil {
			return nil, nil
		}
		if a, ok := collapsed.([]any); ok {
			arr = a
		} else {
			arr = []any{collapsed}
			wasArray = false
		}
	default:
		arr = []any{items}
		wasArray = false
	}

	if len(node.Terms) == 0 {
		if !wasArray && len(arr) == 1 {
			return arr[0], nil
		}
		return arr, nil
	}

	sorted := slices.Clone(arr)

	ctx := env.Context()
	if err := SortItemsErr(sorted, func(a, b any) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return compareSortTerms(node.Terms, a, b, env, env)
	}); err != nil {
		return nil, err
	}
	if !wasArray && len(sorted) == 1 {
		return sorted[0], nil
	}
	return sorted, nil
}

// SortItemsErr performs a stable sort on items using the provided comparator,
// propagating the first error encountered. Only the sign of cmp's return matters:
// negative means a < b, zero or positive means a >= b (sort.SliceStable only
// needs a less-than predicate, so +1 vs 0 is never distinguished).
func SortItemsErr[T any](items []T, cmp func(a, b T) (int, error)) error {
	var sortErr error
	sort.SliceStable(items, func(i, j int) bool {
		if sortErr != nil {
			return false
		}
		c, err := cmp(items[i], items[j])
		if err != nil {
			sortErr = err
			return false
		}
		return c < 0
	})
	return sortErr
}

// compareSortTerms evaluates sort terms against two values and returns the
// comparison result. Each value may have its own environment (for parent-tracking
// sorts where each item carries its lexical scope).
func compareSortTerms(terms []parser.SortTerm, aVal, bVal any, aEnv, bEnv *Environment) (int, error) {
	for _, term := range terms {
		av, err := Eval(term.Expression, aVal, aEnv)
		if err != nil {
			return 0, err
		}
		bv, err := Eval(term.Expression, bVal, bEnv)
		if err != nil {
			return 0, err
		}
		if cmp, err := compareOrder(av, bv); err != nil {
			return 0, err
		} else if cmp != 0 {
			if term.Descending {
				return -cmp, nil
			}
			return cmp, nil
		}
	}
	return 0, nil
}
