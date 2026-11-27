package gnata

import (
	"encoding/json"
	"slices"
	"sync/atomic"

	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
)

// rootKeyCache holds rootKeysIn's answers for an Expression: one for the
// builtins (Expression.EvalMap, a stream without custom functions) and one
// for the last custom environment asked, so an Expression shared between the
// two isn't worked out again on every call.
type rootKeyCache struct {
	builtin atomic.Pointer[rootKeySet]
	custom  atomic.Pointer[rootKeySet]
}

// rootKeySet is the top-level input keys an expression reads in env. complete
// is false when the expression may read keys it doesn't name, so its input
// can't be pruned.
type rootKeySet struct {
	env      *evaluator.Environment
	keys     []string
	complete bool
}

// rootKeysIn returns the top-level input keys e reads when evaluated in env
// (nil for the builtins), from ReferencedPathsIn: evaluating e against its
// input pruned to those keys gives the same result when complete is true.
func (e *Expression) rootKeysIn(env *evaluator.Environment) (keys []string, complete bool) {
	var slot *atomic.Pointer[rootKeySet]
	if e.rootKeys != nil {
		slot = &e.rootKeys.custom
		if env == nil {
			slot = &e.rootKeys.builtin
		}
		if cached := slot.Load(); cached != nil && cached.env == env {
			return cached.keys, cached.complete
		}
	}
	lookup := env
	if lookup == nil {
		lookup = builtinEnv
	}
	paths, complete := parser.ReferencedPaths(e.ast, readsContextIn(lookup))
	keys = make([]string, 0, len(paths))
	for _, path := range paths {
		if len(path) == 0 {
			complete = false
			break
		}
		if !slices.Contains(keys, path[0]) {
			keys = append(keys, path[0])
		}
	}
	if slot != nil {
		slot.Store(&rootKeySet{env: env, keys: keys, complete: complete})
	}
	return keys, complete
}

// decodeMapFor decodes the keys of mapData that expressions[i] for each i in
// indices read, or all of mapData when one of them may read keys it doesn't
// name. Large values no expression reads (a long history beside the few
// small fields the expressions check) are then never decoded. Indices out
// of range and nil expressions are skipped.
func decodeMapFor(
	mapData map[string]json.RawMessage, expressions []*Expression, indices []int, env *evaluator.Environment,
) (*evaluator.OrderedMap, error) {
	// The keys read usually number a handful; a stack buffer holds up to 16
	// without allocating, and more spill to the heap.
	var buf [16]string
	read := buf[:0]
	for _, idx := range indices {
		if idx < 0 || idx >= len(expressions) || expressions[idx] == nil {
			continue
		}
		keys, complete := expressions[idx].rootKeysIn(env)
		if !complete {
			return evaluator.DecodeRawMap(mapData)
		}
		for _, key := range keys {
			if _, ok := mapData[key]; ok && !slices.Contains(read, key) {
				read = append(read, key)
			}
		}
	}
	if len(read) == len(mapData) {
		return evaluator.DecodeRawMap(mapData)
	}
	needed := make(map[string]json.RawMessage, len(read))
	for _, key := range read {
		needed[key] = mapData[key]
	}
	return evaluator.DecodeRawMap(needed)
}
