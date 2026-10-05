package evaluator

import (
	"cmp"
	"encoding/json"

	"github.com/recolabs/gnata/internal/decimal"
	"github.com/recolabs/gnata/internal/parser"
)

// evalNumber returns a number literal. With decimal precision enabled it is a
// json.Number rounded and canonicalised (1.50 becomes 1.5), unless out of
// range.
func evalNumber(node *parser.Node, env *Environment) any {
	prec := env.DecimalPrecision()
	if prec == 0 {
		return node.NumVal
	}
	if decimal.IsCanonical(node.Value, prec) {
		return json.Number(node.Value)
	}
	if d, ok := decimal.Parse(node.Value, prec); ok {
		return d.Value(prec)
	}
	return node.NumVal
}

// DecimalCmp compares two numbers in decimal. ok is false when either is not a
// number in range, in which case callers fall back to float64 comparison.
func DecimalCmp(a, b any, prec int) (int, bool) {
	if af, ok := a.(float64); ok {
		if bf, ok := b.(float64); ok {
			return cmp.Compare(af, bf), true
		}
	}
	x, ok := decimal.FromValue(a, prec)
	if !ok {
		return 0, false
	}
	y, ok := decimal.FromValue(b, prec)
	if !ok {
		return 0, false
	}
	return x.Cmp(y), true
}

// decimalOrder compares two numbers in decimal when either is a json.Number.
// ok is false otherwise, or when either is not a number in range.
func decimalOrder(a, b any, prec int) (int, bool) {
	_, an := a.(json.Number)
	_, bn := b.(json.Number)
	if !an && !bn {
		return 0, false
	}
	return DecimalCmp(a, b, prec)
}

func decimalEqual(a, b any, prec int) (equal, ok bool) {
	c, ok := decimalOrder(a, b, prec)
	return c == 0, ok
}

// DecimalArith applies an arithmetic operator in decimal, rounding half to even
// to prec significant digits. The error is decimal.ErrOverflow for a result
// beyond the float64 range, which float64 arithmetic may miss after rounding
// the operands, and decimal.ErrUndefined when an operand is not a number in
// range or the operation has no result (e.g. division by zero); callers then
// fall back to float64 arithmetic, which reports the same error.
func DecimalArith(l, r any, op string, prec int) (any, error) {
	x, ok := decimal.FromValue(l, prec)
	if !ok {
		return nil, decimal.ErrUndefined
	}
	y, ok := decimal.FromValue(r, prec)
	if !ok {
		return nil, decimal.ErrUndefined
	}
	var z decimal.Decimal
	var err error
	switch op {
	case "+":
		z, err = x.Add(y, prec)
	case "-":
		z, err = x.Sub(y, prec)
	case "*":
		z, err = x.Mul(y, prec)
	case "/":
		z, err = x.Quo(y, prec)
	case "%":
		z, err = x.Rem(y, prec)
	case "**":
		z, err = x.Pow(y, prec)
	default:
		err = decimal.ErrUndefined
	}
	if err != nil {
		return nil, err
	}
	return z.Value(prec), nil
}

// FormatDecimal lays out a number as JavaScript does, but with every digit it
// has under decimal precision prec; a float64 counts as its shortest decimal
// form. ok is false when prec is 0 or v is not a number in range.
func FormatDecimal(v any, prec int) (string, bool) {
	if prec == 0 {
		return "", false
	}
	d, ok := decimal.FromValue(v, prec)
	if !ok {
		return "", false
	}
	return d.JSString(), true
}
