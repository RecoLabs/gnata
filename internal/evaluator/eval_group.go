package evaluator

import (
	"fmt"
	"iter"

	"github.com/recolabs/gnata/internal/parser"
)

type groupEntry struct {
	items    []any
	firstIdx int
}

func evalGroupBy(node *parser.Node, input any, env *Environment) (any, error) {
	// For paths with #$var position bindings or % references (in steps or in the
	// group expression itself), delegate directly to evalPathTuple which handles
	// the group expression with per-tuple environments.
	if node.Type == parser.NodePath && (pathHasTupleStep(node.Steps) || groupHasParentRef(node.Group)) {
		return evalPathTuple(node, input, env)
	}

	// Copy the node with Group cleared to evaluate the base expression without
	// recursion and without mutating the shared AST (concurrent safety).
	baseCopy := *node
	baseCopy.Group = nil
	base, err := Eval(&baseCopy, input, env)
	if err != nil || base == nil {
		return nil, err
	}
	// jsonata-js's trampoline applies a tail-position call without its
	// group, so the group is ignored.
	if _, tailCall := base.(*TailCall); tailCall {
		return base, nil
	}

	var items []any
	if seq, ok := base.(*Sequence); ok {
		if base = CollapseSequence(seq); base == nil {
			return nil, nil
		}
	}
	if arr, ok := AsArray(base); ok {
		items = arr
	} else {
		items = []any{base}
	}
	return groupItems(groupPairs(node.Group.Pairs), items, env)
}

// groupPairs yields the key and value expressions of a parsed group.
func groupPairs(pairs [][2]*parser.Node) iter.Seq2[*parser.Node, *parser.Node] {
	return func(yield func(key, value *parser.Node) bool) {
		for _, pair := range pairs {
			if !yield(pair[0], pair[1]) {
				return
			}
		}
	}
}

// objectPairs yields the key and value expressions of an object
// constructor's flat [k, v, k, v, ...] operands in place.
func objectPairs(flat []*parser.Node) iter.Seq2[*parser.Node, *parser.Node] {
	return func(yield func(key, value *parser.Node) bool) {
		for i := 0; i+1 < len(flat); i += 2 {
			if !yield(flat[i], flat[i+1]) {
				return
			}
		}
	}
}

// groupContext folds the items sharing a group key into the context of the
// group's value expression as jsonata-js fn.append does: a lone item is kept
// as is, while array items are concatenated rather than nested.
func groupContext(items []any) any {
	if len(items) == 1 {
		return items[0]
	}
	var merged []any
	for _, item := range items {
		if arr, ok := AsArray(item); ok {
			merged = append(merged, arr...)
		} else if item != nil {
			merged = append(merged, item)
		}
	}
	return merged
}

// groupItems builds the {key: value} object of a group expression over items.
func groupItems(pairs iter.Seq2[*parser.Node, *parser.Node], items []any, env *Environment) (any, error) {
	outObj, keySet := NewOrderedMap(), map[string]bool{}
	for keyNode, valNode := range pairs {
		var groupOrder []string
		groups := map[string]*groupEntry{}

		for i, item := range items {
			keyVal, err := Eval(keyNode, item, env)
			if err != nil {
				return nil, err
			}
			keyStr, ok := keyVal.(string)
			if keyVal == nil {
				continue
			} else if !ok {
				return nil, &JSONataError{Code: "T1003", Message: fmt.Sprintf("key expression must evaluate to a string, got %T", keyVal)}
			}
			if g, exists := groups[keyStr]; exists {
				g.items = append(g.items, item)
			} else {
				groupOrder = append(groupOrder, keyStr)
				groups[keyStr] = &groupEntry{items: []any{item}, firstIdx: i}
			}
		}

		for _, keyStr := range groupOrder {
			if keySet[keyStr] {
				return nil, &JSONataError{Code: "D1009", Message: fmt.Sprintf("duplicate key: %q", keyStr)}
			}
			entry := groups[keyStr]
			groupInput := groupContext(entry.items)

			childEnv := NewChildEnvironment(env)
			childEnv.Bind("$index", float64(entry.firstIdx))
			childEnv.Bind("$key", keyStr)

			valResult := groupInput
			if valNode != nil {
				var err error
				if valResult, err = Eval(valNode, groupInput, childEnv); err != nil {
					return nil, err
				}
			}
			if valResult != nil {
				keySet[keyStr] = true
				outObj.Set(keyStr, valResult)
			}
		}
	}
	return outObj, nil
}
