package evaluator

import (
	"fmt"

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

	var items []any
	switch v := base.(type) {
	case []any:
		items = v
	case *Sequence:
		if collapsed := CollapseSequence(v); collapsed == nil {
			return nil, nil
		} else if arr, ok := collapsed.([]any); ok {
			items = arr
		} else {
			items = []any{collapsed}
		}
	default:
		items = []any{base}
	}

	outObj, keySet := NewOrderedMap(), map[string]bool{}
	for _, pair := range node.Group.Pairs {
		keyNode, valNode := pair[0], pair[1]
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
			groupInput := any(entry.items)
			if len(entry.items) == 1 {
				groupInput = entry.items[0]
			}

			childEnv := NewChildEnvironment(env)
			childEnv.Bind("$index", float64(entry.firstIdx))
			childEnv.Bind("$key", keyStr)

			valResult := groupInput
			if valNode != nil {
				if valResult, err = Eval(valNode, groupInput, childEnv); err != nil {
					return nil, err
				}
				if valNode.KeepArray && valResult == nil {
					valResult = []any{}
				} else if valNode.KeepArray {
					if _, isArr := valResult.([]any); !isArr {
						valResult = []any{valResult}
					}
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
