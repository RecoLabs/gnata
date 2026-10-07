package evaluator

import (
	"fmt"
	"iter"

	"github.com/recolabs/gnata/internal/parser"
)

func evalGroupBy(node *parser.Node, input any, env *Environment) (any, error) {
	// A path with #$var, @$var or ancestor bindings runs as a tuple stream,
	// whose evalPathTuple applies the group with per-tuple environments.
	if node.Type == parser.NodePath && pathHasTupleStep(node.Steps) {
		return evalPathTuple(node, input, env)
	}

	// Copy the node with Group cleared to evaluate the base expression without
	// recursion and without mutating the shared AST (concurrent safety).
	baseCopy := *node
	baseCopy.Group = nil
	var base any
	var err error
	switch baseCopy.Type {
	case parser.NodeFunction:
		base, err = evalFunctionSequence(&baseCopy, input, env)
	case parser.NodeSort:
		base, err = evalSortStep(&baseCopy, input, env)
	case parser.NodePath:
		if isSimplePath(&baseCopy) {
			base, err = evalPathSequence(&baseCopy, input, env)
			break
		}
		base, err = Eval(&baseCopy, input, env)
	default:
		base, err = Eval(&baseCopy, input, env)
	}
	if err != nil {
		return nil, err
	}
	// jsonata-js's trampoline applies a tail-position call without its
	// group, so the group is ignored.
	if _, tailCall := base.(*TailCall); tailCall {
		return base, nil
	}

	// A call's or sort's result sequence is grouped before it collapses, as
	// in jsonata-js.
	if seq, ok := base.(*Sequence); ok && len(seq.Values) > 0 &&
		(baseCopy.Type == parser.NodeFunction || baseCopy.Type == parser.NodeSort) {
		return groupItems(groupPairs(node.Group.Pairs), seq.Values, env)
	}
	if seq, ok := base.(*Sequence); ok {
		base = CollapseSequence(seq)
	}
	items, ok := AsArray(base)
	if !ok {
		items = []any{base}
	} else {
		// A nil item of Go data is a JSON null, not an undefined context.
		items = NullItems(items)
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
	merged := []any{}
	for _, item := range items {
		if arr, ok := AsArray(item); ok {
			merged = append(merged, arr...)
		} else if item != nil {
			merged = append(merged, item)
		}
	}
	return merged
}

// groupItems builds the {key: value} object of a group expression over
// items, as jsonata-js evaluateGroupExpression does. Each item is keyed by
// every pair in turn, so keys appear in the order items produce them, and a
// key two pairs produce raises D1009. An empty input groups one undefined
// item, so constant keys still yield an object: []{"a": 1} is {"a":1}.
func groupItems(pairs iter.Seq2[*parser.Node, *parser.Node], items []any, env *Environment) (any, error) {
	if len(items) == 0 {
		items = []any{nil}
	}
	return groupBy(pairs, items, nil, env)
}

// groupEntry collects the items one key of a group gathers.
type groupEntry struct {
	key   string
	pair  int          // which pair produced the key
	value *parser.Node // that pair's value expression
	items []any
	envs  []*Environment
	size  appendCount
}

// appendCount counts the items of values appended in turn as jsonata-js
// fn.append does. The first value is taken as is, so only appending a later
// one counts against the sequence guardrail.
type appendCount struct{ items, values int }

func (c *appendCount) add(v any, env *Environment) error {
	if !env.sequenceLimited {
		return nil
	}
	c.items += appendLength(v)
	c.values++
	if c.values == 1 {
		return nil
	}
	return env.CheckSequence(c.items)
}

// groupBy groups items by each pair's key and evaluates each key's value
// expression against its items. With envs, the items are tuples, keyed in
// their own environments, and each group's value sees their bindings merged
// (see mergeGroupEnvs); otherwise every expression runs in env.
func groupBy(pairs iter.Seq2[*parser.Node, *parser.Node], items []any, envs []*Environment, env *Environment) (any, error) {
	var order []*groupEntry
	byKey := map[string]*groupEntry{}
	for i, item := range items {
		itemEnv := env
		if envs != nil {
			itemEnv = envs[i]
		}
		pair := 0
		for keyNode, valNode := range pairs {
			index := pair
			pair++
			keyVal, err := Eval(keyNode, item, itemEnv)
			if err != nil {
				return nil, err
			}
			if keyVal == nil {
				continue
			}
			key, ok := keyVal.(string)
			if !ok {
				return nil, &JSONataError{Code: "T1003", Message: fmt.Sprintf("key expression must evaluate to a string, got %T", keyVal)}
			}
			entry := byKey[key]
			if entry == nil {
				entry = &groupEntry{key: key, pair: index, value: valNode}
				byKey[key] = entry
				order = append(order, entry)
			} else if entry.pair != index {
				return nil, &JSONataError{Code: "D1009", Message: fmt.Sprintf("duplicate key: %q", key)}
			}
			if err := entry.size.add(item, itemEnv); err != nil {
				return nil, err
			}
			entry.items = append(entry.items, item)
			if envs != nil {
				entry.envs = append(entry.envs, itemEnv)
			}
		}
	}

	out := NewOrderedMap()
	for _, entry := range order {
		valEnv := env
		if envs != nil {
			valEnv = mergeGroupEnvs(entry.envs)
		}
		val := groupContext(entry.items)
		if entry.value != nil {
			var err error
			if val, err = Eval(entry.value, val, valEnv); err != nil {
				return nil, err
			}
		}
		if val != nil {
			out.Set(entry.key, val)
		}
	}
	return out, nil
}
