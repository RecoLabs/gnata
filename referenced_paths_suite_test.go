package gnata_test

import (
	"context"
	"testing"

	"github.com/recolabs/gnata"
)

// checkReferencedPaths requires that an expression whose ReferencedPaths
// are complete evaluates the same against its input pruned to those paths.
func checkReferencedPaths(t *testing.T, src string, expr *gnata.Expression, input any, bindings map[string]any, want any) {
	t.Helper()
	paths, complete := expr.ReferencedPaths()
	if !complete {
		return
	}
	trie := pathTrie{}
	for _, path := range paths {
		trie.add(path)
	}
	got, err := expr.EvalWithVars(context.Background(), trie.prune(input), bindings)
	if err != nil || !gnata.DeepEqual(got, want) {
		t.Errorf("ReferencedPaths %v missed a read:\n  expr: %s\n  want: %v\n  got:  %v, %v", paths, src, want, got, err)
	}
}

// pathTrie keeps the input under its paths. A nil child map marks a path's
// end, whose whole value is kept.
type pathTrie map[string]pathTrie

func (p pathTrie) add(path []string) {
	node := p
	for i, name := range path {
		child, ok := node[name]
		switch {
		case ok && child == nil:
			return
		case i == len(path)-1:
			node[name] = nil
			return
		case !ok:
			child = pathTrie{}
			node[name] = child
		}
		node = child
	}
}

// prune keeps only the fields of v under p, mapping over arrays the way a
// path does.
func (p pathTrie) prune(v any) any {
	switch value := v.(type) {
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = p.prune(item)
		}
		return out
	case *gnata.OrderedMap:
		out := gnata.NewOrderedMap()
		value.Range(func(key string, val any) bool {
			if child, ok := p[key]; ok {
				if child == nil {
					out.Set(key, val)
				} else {
					out.Set(key, child.prune(val))
				}
			}
			return true
		})
		return out
	case map[string]any:
		out := map[string]any{}
		for key, val := range value {
			if child, ok := p[key]; ok {
				if child == nil {
					out[key] = val
				} else {
					out[key] = child.prune(val)
				}
			}
		}
		return out
	}
	return v
}
