package parser

import "testing"

// A new FuncFastKind must be classified for WithDecimalPrecision, or it stays
// on the float64 fast path unnoticed.
func TestDecimalSafeFuncKindsClassifyEveryKind(t *testing.T) {
	for kind := FuncFastExists; kind <= FuncFastAverage; kind++ {
		if kind == FuncFastRound { // reserved, never produced
			continue
		}
		if _, ok := decimalSafeFuncKinds[kind]; !ok {
			t.Errorf("FuncFastKind %d ($%s) is missing from decimalSafeFuncKinds", kind, funcFastNames[kind])
		}
	}
}
