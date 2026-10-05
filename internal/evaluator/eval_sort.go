package evaluator

import (
	"slices"
	"sort"

	"github.com/recolabs/gnata/internal/parser"
)

func evalSort(node *parser.Node, input any, env *Environment) (any, error) {
	result, err := evalSortStage(node, input, env)
	if seq, ok := result.(*Sequence); ok {
		return CollapseSequence(seq), err
	}
	return result, err
}

// evalSortStep evaluates a sort as a path step or a subscript's operand,
// whose result stays a sequence; a sort referencing % tracks its parents.
func evalSortStep(node *parser.Node, input any, env *Environment) (any, error) {
	if sortHasParentRef(node) {
		return Eval(node, input, env)
	}
	return evalSortStage(node, input, env)
}

func sortHasParentRef(node *parser.Node) bool {
	return slices.ContainsFunc(node.Terms, func(t parser.SortTerm) bool { return nodeHasParentRef(t.Expression) })
}

// evalSortStage sorts node's operand. As in jsonata-js, the result is a
// sequence, which collapses to a lone item where an expression ends.
func evalSortStage(node *parser.Node, input any, env *Environment) (any, error) {
	var items any
	var err error
	if isSubscript(node.Left) {
		items, err = evalSubscriptStage(node.Left, input, env)
	} else {
		items, err = Eval(node.Left, input, env)
	}
	if err != nil || items == nil {
		return nil, err
	}

	var arr []any
	switch v := items.(type) {
	case []any:
		arr = v
	case *Sequence:
		arr = v.Values
	default:
		arr = []any{items}
	}
	if err := env.CheckSequence(len(arr)); err != nil {
		return nil, err
	}

	sorted := slices.Clone(arr)
	if len(node.Terms) > 0 {
		if err := SortItemsErr(sorted, func(a, b any) (int, error) {
			if err := env.Err(); err != nil {
				return 0, err
			}
			return compareSortTerms(node.Terms, a, b, env, env)
		}); err != nil {
			return nil, err
		}
	}
	if node.KeepArray {
		return sorted, nil
	}
	return &Sequence{Values: sorted}, nil
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
		if cmp, err := compareOrder(av, bv, aEnv.DecimalPrecision()); err != nil {
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
