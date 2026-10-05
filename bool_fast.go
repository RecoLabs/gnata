package gnata

import (
	"encoding/json"

	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
)

func evalBool(b *parser.BoolFastPath, data json.RawMessage, mapData map[string]json.RawMessage) (result any, handled bool, err error) {
	switch b.Op {
	case parser.BoolFastAnd, parser.BoolFastOr:
		left, ok, evalErr := evalBool(b.Left, data, mapData)
		if !ok || evalErr != nil {
			return nil, ok, evalErr
		}
		leftTrue := evaluator.ToBoolean(left)
		if b.Op == parser.BoolFastAnd && !leftTrue {
			return false, true, nil
		}
		if b.Op == parser.BoolFastOr && leftTrue {
			return true, true, nil
		}
		right, ok, evalErr := evalBool(b.Right, data, mapData)
		if !ok || evalErr != nil {
			return nil, ok, evalErr
		}
		return evaluator.ToBoolean(right), true, nil
	case parser.BoolFastNot:
		operand, ok, evalErr := evalBool(b.Left, data, mapData)
		if !ok || evalErr != nil {
			return nil, ok, evalErr
		}
		if operand == nil {
			return nil, true, nil
		}
		return !evaluator.ToBoolean(operand), true, nil
	case parser.BoolFastLeaf:
		return evalBoolLeaf(b, data, mapData)
	}
	return nil, false, nil
}

func evalBoolLeaf(b *parser.BoolFastPath, data json.RawMessage, mapData map[string]json.RawMessage) (result any, handled bool, err error) {
	switch {
	case b.Cmp != nil:
		return evalComparison(b.Cmp, data, mapData)
	case b.Func != nil:
		return evalFunc(b.Func, data, mapData)
	case b.PurePath != "":
		// and / or / $not only test a leaf for truthiness, which a number has
		// alike as float64 or json.Number.
		value, ok := resolvePurePath(b.PurePath, b.PureSteps, data, mapData, false)
		return value, ok, nil
	}
	return nil, false, nil
}
