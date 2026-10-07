package evaluator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
	"unsafe"

	"github.com/recolabs/gnata/internal/parser"
)

func appendToSequence(seq *Sequence, v any) {
	if v == nil {
		return
	}
	switch val := v.(type) {
	case *Sequence:
		for _, item := range val.Values {
			appendToSequence(seq, item)
		}
	default:
		seq.Values = append(seq.Values, v)
	}
}

func stringifyValue(v any, env *Environment) (string, error) {
	if v == nil {
		return "", nil
	}
	prec := env.DecimalPrecision()
	switch val := v.(type) {
	case string:
		return val, nil
	case json.Number:
		if s, ok := FormatDecimal(val, prec); ok {
			return s, nil
		}
		return FormatNumber(val), nil
	case float64:
		if s, ok := FormatDecimal(val, prec); ok {
			return s, nil
		}
		return FormatFloat(val), nil
	case bool:
		if val {
			return "true", nil
		}
		return "false", nil
	default:
		if IsFunction(v) {
			return "", nil
		}
		prepared, err := JSONValue(v, env)
		if err != nil {
			return "", err
		}
		b, err := AppendJSON(nil, prepared)
		if err != nil {
			return "", fmt.Errorf("cannot stringify value: %w", err)
		}
		return string(b), nil
	}
}

// FormatNumber converts a json.Number to the string $string gives for it.
// jsonata-js reads every input number as a JavaScript Number, so the number
// is formatted through float64 as FormatFloat does (1.50 becomes "1.5"),
// except that an integer literal beyond 2^53 keeps its digits (see
// isLargeInteger), where jsonata-js would round 12345678901234567890 to
// "12345678901234567000". Text that does not parse as a float64 is returned
// verbatim.
func FormatNumber(n json.Number) string {
	if isLargeInteger(n) {
		return string(n)
	}
	f, err := n.Float64()
	if err != nil {
		return n.String()
	}
	return FormatFloat(f)
}

// maxSafeInteger is 2^53, past which float64 cannot hold every integer.
const maxSafeInteger = "9007199254740992"

// isLargeInteger reports whether n is an integer literal, with no fraction
// or exponent, whose magnitude is above 2^53, which float64 may round, as
// raw JSON input such as a large id has. $string and & print its digits as
// they are.
func isLargeInteger(n json.Number) bool {
	digits := strings.TrimPrefix(string(n), "-")
	if len(digits) < len(maxSafeInteger) || digits[0] == '0' {
		return false
	}
	for i := range len(digits) {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return len(digits) > len(maxSafeInteger) || digits > maxSafeInteger
}

// FormatFloat converts a float64 to the string jsonata-js's $string gives:
// JavaScript's Number.toString() of roundJS(n), which uses decimal notation
// from 1e-6 up to 1e21 and scientific notation outside it.
func FormatFloat(n float64) string {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return "null"
	}
	var buf [32]byte
	return string(appendJSNumber(buf[:0], n))
}

// appendJSNumber appends FormatFloat(n) for a finite n.
func appendJSNumber(b []byte, n float64) []byte {
	if n == 0 {
		return append(b, '0')
	}
	if n < 0 {
		b = append(b, '-')
	}
	var buf [32]byte
	digits, exp := roundedDigits(buf[:0], n)
	point := exp + 1 // digits before the decimal point
	switch {
	case point >= 22 || point <= -6:
		b = append(b, digits[0])
		if len(digits) > 1 {
			b = append(append(b, '.'), digits[1:]...)
		}
		b = append(b, 'e')
		if exp > 0 {
			b = append(b, '+')
		}
		return strconv.AppendInt(b, int64(exp), 10)
	case point <= 0:
		b = append(b, "0."...)
		for range -point {
			b = append(b, '0')
		}
		return append(b, digits...)
	case point >= len(digits):
		b = append(b, digits...)
		for range point - len(digits) {
			b = append(b, '0')
		}
		return b
	}
	return append(append(append(b, digits[:point]...), '.'), digits[point:]...)
}

// roundedDigits returns the significant digits of roundJS(n), for a finite
// nonzero n, without trailing zeros, and the decimal exponent of the first,
// in buf. A decimal of at most 15 significant digits is the shortest form
// of the float nearest it, so these are the shortest digits of roundJS(n).
func roundedDigits(buf []byte, n float64) (digits []byte, exp int) {
	abs := math.Abs(n)
	var e []byte
	switch {
	case n == math.Trunc(n):
		e = strconv.AppendFloat(buf, abs, 'e', -1, 64)
	case abs < 0x1p-1022:
		// A subnormal's 15 digits may be finer than its precision, so the
		// float nearest them can have a different shortest form.
		r := strconv.AppendFloat(buf, abs, 'e', 14, 64)
		rounded, _ := strconv.ParseFloat(unsafe.String(&r[0], len(r)), 64)
		e = strconv.AppendFloat(buf[:0], rounded, 'e', -1, 64)
	default:
		if isPrecision15Tie(abs) {
			// toPrecision rounds an exact tie away from zero, where strconv
			// rounds it to even; one ulp up breaks the tie that way.
			abs = math.Nextafter(abs, math.Inf(1))
		}
		e = strconv.AppendFloat(buf, abs, 'e', 14, 64)
	}
	mark := bytes.IndexByte(e, 'e')
	for _, c := range e[mark+2:] {
		exp = exp*10 + int(c-'0')
	}
	if e[mark+1] == '-' {
		exp = -exp
	}
	digits = e[:mark]
	if len(digits) > 1 {
		digits = append(digits[:1], digits[2:]...) // drop the point
	}
	for len(digits) > 1 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}
	return digits, exp
}

// roundJS rounds n as jsonata-js does before turning a number into JSON: a
// non-integer to 15 significant digits (Number(n.toPrecision(15))), and -0
// to 0, which JSON.stringify writes as 0.
func roundJS(n float64) float64 {
	if n == 0 {
		return 0
	}
	if n == math.Trunc(n) || math.IsInf(n, 0) || math.IsNaN(n) {
		return n
	}
	var buf [32]byte
	// A float whose shortest form has at most 15 significant digits, a
	// mantissa d.ddd of at most 16 bytes, is its own 15-digit rounding.
	if shortest := strconv.AppendFloat(buf[:0], math.Abs(n), 'e', -1, 64); bytes.IndexByte(shortest, 'e') <= 16 {
		return n
	}
	b := appendJSNumber(buf[:0], n)
	// The rounded digits always parse.
	rounded, _ := strconv.ParseFloat(unsafe.String(&b[0], len(b)), 64)
	return rounded
}

// fractionBits returns how many binary digits f has after the point.
func fractionBits(f float64) int {
	frac, exp := math.Frexp(f) // f = frac × 2^exp, 0.5 ≤ frac < 1
	mantissa := uint64(frac * (1 << 53))
	return 53 - exp - bits.TrailingZeros64(mantissa)
}

// isPrecision15Tie reports whether abs is exactly halfway between two
// 15-significant-digit decimals: its 16th digit is 5 and every later one 0.
// A float64 that is not an exact tie differs from one by the 18th digit, so
// 25 digits are enough to tell.
func isPrecision15Tie(abs float64) bool {
	// A tie has 16 significant digits. A non-integer with k binary digits
	// after the point has k decimal digits after it, so k ≤ 22, and 16
	// significant digits only in [10^(15-k), 10^(16-k)), widened here for
	// float64 powers of ten; this skips the slow 25-digit format for almost
	// every float.
	k := fractionBits(abs)
	if k > 22 || abs < math.Pow10(15-k)*0.99 || abs >= math.Pow10(16-k)*1.01 {
		return false
	}
	s := strconv.FormatFloat(abs, 'e', 24, 64)
	digits := s[:1] + s[2:26]
	return digits[15] == '5' && strings.Trim(digits[16:], "0") == ""
}

// applyCmpOp applies an ordering operator to the result c of a three-way
// comparison (-1, 0 or +1).
func applyCmpOp(c int, op string) bool {
	switch op {
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	}
	return false
}

func compareValues(left, right any, op string) (any, error) {
	_, leftIsNum := ToFloat64(left)
	_, leftIsStr := left.(string)
	if left != nil && !leftIsNum && !leftIsStr {
		return nil, &JSONataError{Code: "T2010", Message: fmt.Sprintf("the operands of the %q operator must be numbers or strings", op)}
	}
	if left == nil || right == nil {
		return nil, nil
	}
	if ln, lok := ToFloat64(left); lok {
		if rn, rok := ToFloat64(right); rok {
			switch op {
			case "<":
				return ln < rn, nil
			case "<=":
				return ln <= rn, nil
			case ">":
				return ln > rn, nil
			case ">=":
				return ln >= rn, nil
			}
		}
		if _, isStr := right.(string); isStr {
			return nil, &JSONataError{
				Code:    "T2009",
				Message: fmt.Sprintf("the operands of the %q operator must be both numbers or both strings", op),
			}
		}
		return nil, &JSONataError{Code: "T2010", Message: fmt.Sprintf("the operands of the %q operator must be numbers or strings", op)}
	}
	if ls, lok := left.(string); lok {
		if rs, rok := right.(string); rok {
			return applyCmpOp(strings.Compare(ls, rs), op), nil
		}
		if _, isNum := ToFloat64(right); isNum {
			return nil, &JSONataError{
				Code:    "T2009",
				Message: fmt.Sprintf("the operands of the %q operator must be both numbers or both strings", op),
			}
		}
		return nil, &JSONataError{Code: "T2010", Message: fmt.Sprintf("the operands of the %q operator must be numbers or strings", op)}
	}
	return nil, &JSONataError{Code: "T2010", Message: fmt.Sprintf("the operands of the %q operator must be numbers or strings", op)}
}

func compareOrder(a, b any, prec int) (int, error) {
	if a == nil && b == nil {
		return 0, nil
	}
	if a == nil {
		return 1, nil
	}
	if b == nil {
		return -1, nil
	}
	if prec > 0 {
		if c, ok := DecimalCmp(a, b, prec); ok {
			return c, nil
		}
	}
	an, aNum := ToFloat64(a)
	bn, bNum := ToFloat64(b)
	if aNum && bNum {
		if an < bn {
			return -1, nil
		} else if an > bn {
			return 1, nil
		}
		return 0, nil
	}
	as, aStr := a.(string)
	bs, bStr := b.(string)
	if aStr && bStr {
		if as < bs {
			return -1, nil
		} else if as > bs {
			return 1, nil
		}
		return 0, nil
	}
	if (aNum && bStr) || (aStr && bNum) {
		return 0, &JSONataError{Code: "T2007", Message: "cannot compare string and number values"}
	}
	return 0, &JSONataError{Code: "T2008", Message: fmt.Sprintf("cannot compare values of type %T and %T", a, b)}
}

func containsValue(arr, elem any, env *Environment) (bool, error) {
	var items []any
	switch v := arr.(type) {
	case nil:
		return false, nil
	case []any, ConsArray, KeptArray, RawSequence:
		items, _ = AsArray(v)
	case *Sequence:
		items = v.Values
	default:
		return DeepEqualEnv(arr, elem, env)
	}
	equaler := NewEqualer(env)
	for _, item := range items {
		if equal, err := equaler.Equal(NilAsNull(item), elem); equal || err != nil {
			return equal, err
		}
	}
	return false, nil
}

func evalValue(node *parser.Node) (any, error) {
	switch node.Value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case parser.NullJSON:
		return Null, nil
	default:
		return nil, nil
	}
}

func evalVariable(node *parser.Node, input any, env *Environment) (any, error) {
	if node.Value == "" {
		return settleRaw(input, node.KeepArray), nil
	}
	val, found := env.Lookup(node.Value)
	if !found {
		return nil, nil
	}
	return settleRaw(val, node.KeepArray), nil
}

// evalName's array-mapping semantics (flatten every level of an array
// context, collapse the sequence once, so a field holding only empty
// arrays is undefined)
// and nameOverContexts' (collapse each context's sequence, return a last
// step's lone array as is) are mirrored by path_bytes.go's
// walkPureSteps/stepArray for the gjson-based fast path. Keep the two in
// sync: a change here needs the matching change there (internal array
// types never occur in decoded input, so their handling is evaluator-only).
func evalName(node *parser.Node, input any, env *Environment) (any, error) {
	val, items := nameLookup(node.Value, input, false)
	if items == nil {
		return val, nil
	}
	frame, err := nameOverArray(node.Value, items, env)
	if err != nil {
		return nil, err
	}
	return frame.collapse(node.KeepArray), nil
}

// evalBoundedName evaluates a field lookup outside a path step, which
// jsonata-js evaluates as a one-step path, against the sequence guardrail
// (see evalNameStep). Over the root array, which jsonata-js wraps as one
// context, the lookup builds a sequence.
func evalBoundedName(node *parser.Node, input any, env *Environment) (any, error) {
	if items, ok := input.([]any); ok && isRootInput(items, env) {
		return evalLookup(node, input, env)
	}
	result, err := evalNameStep(node, input, env, true)
	return CollapseSequences(result), err
}

// evalNameStep evaluates a field step over its contexts: an array input's
// items (see nameOverContexts), or input itself. Under WithSequence a last
// step's sequence counts against the limit, unless it is a lone context's
// array passed through as is; a step that is not the last is bounded by
// evalPathStep.
func evalNameStep(node *parser.Node, input any, env *Environment, lastStep bool) (any, error) {
	items, isArr := input.([]any)
	if !isArr {
		return evalName(node, input, env)
	}
	result, seqLen, err := nameOverContexts(node, items, lastStep, env)
	if err != nil || !lastStep {
		return result, err
	}
	return result, env.CheckSequence(seqLen)
}

// evalLookup evaluates a field lookup of input as one value, as jsonata-js
// looks up a field of an array context: the lookup builds a sequence,
// bounded by WithSequence.
func evalLookup(node *parser.Node, input any, env *Environment) (any, error) {
	result, err := evalName(node, input, env)
	if err != nil || !env.sequenceLimited {
		return result, err
	}
	if _, isArr := AsArray(CollapseSequences(input)); !isArr {
		return result, nil
	}
	return result, checkSequenceLength(result, env)
}

// nameOverContexts maps the lookup of node's field over items, each a
// context, as jsonata-js evaluateStep does: an array context's lookup is
// one sequence, collapsed (or kept, with []) before the step flattens it
// into its result. A last step returns a lone context's array result as
// is; seqLen is the length of the sequence the step built, which
// WithSequence bounds, or 0 when it passed that array through.
func nameOverContexts(
	node *parser.Node, items []any, lastStep bool, env *Environment,
) (result any, seqLen int, _ error) {
	frame := newNameFrame(nil)
	defined := 0
	var last any
	lastIsSeq := false
	for _, item := range items {
		val, nested := nameLookup(node.Value, item, true)
		isSeq := false
		if nested != nil {
			inner, err := nameOverArray(node.Value, nested, env)
			if err != nil {
				return nil, 0, err
			}
			val, isSeq = inner.collapse(node.KeepArray), len(inner.seq.Values) > 1
		}
		if val == nil {
			continue
		}
		defined++
		last, lastIsSeq = val, isSeq
		frame.add(val)
	}
	if arr, isArr := last.([]any); isArr && !lastIsSeq && lastStep && defined == 1 {
		return arr, 0, nil
	}
	result = frame.collapse(false)
	if _, isArr := result.([]any); isArr && !node.KeepArray && len(frame.seq.Values) == 1 {
		// The step's flattened result holds one array, which the next step,
		// or a sort or group of the path, takes as one context.
		return &Sequence{Values: []any{result}}, 1, nil
	}
	if node.KeepArray && len(frame.seq.Values) == 1 {
		// The [] keeps the step's one-item sequence, which a later step or
		// the path's [] reads as an array holding that item.
		result = []any{result}
	}
	return result, len(frame.seq.Values), nil
}

// nameLookup returns the value of field key in input, or input's items when
// it is an array, which nameOverArray maps the lookup over. An item of an
// array being mapped over keeps a field's internal array type for add to
// flatten, and an empty result sequence there is no value.
func nameLookup(key string, input any, item bool) (val any, items []any) {
	switch v := CollapseSequences(input).(type) {
	case *OrderedMap, map[string]any:
		val, ok := MapGet(v, key)
		switch {
		case !ok:
			return nil, nil
		case !item:
			return fieldValue(val), nil
		case val == nil:
			return Null, nil
		}
		if raw, isRaw := val.(RawSequence); isRaw && len(raw) == 0 {
			return nil, nil
		}
		return val, nil
	case []any, ConsArray, KeptArray, RawSequence:
		arr, _ := AsArray(v)
		if arr == nil {
			return nil, []any{}
		}
		return nil, arr
	}
	return nil, nil
}

// nameFrame is an array nameOverArray is mapping a field lookup over: its
// items, the index of the next one, and the values found so far.
type nameFrame struct {
	items []any
	index int
	seq   Sequence
}

// nameOverArray maps the lookup of field key over items, the items of one
// array context, as jsonata-js lookup does: the values found in items and,
// at any depth, in their nested arrays are flattened into one sequence,
// which the returned frame holds. Nested arrays are mapped from an explicit
// stack of frames rather than by recursion, so deep nesting cannot overflow
// the goroutine stack.
func nameOverArray(key string, items []any, env *Environment) (nameFrame, error) {
	frame := newNameFrame(items)
	for frame.index < len(frame.items) {
		item := frame.items[frame.index]
		frame.index++
		val, nested := nameLookup(key, item, true)
		if nested != nil {
			return nameOverNested(key, frame, nested, env)
		}
		frame.add(val)
	}
	return frame, nil
}

// nameMemoMaxLen is the most values a nested array's lookup can find for
// nameOverNested to memoize them. A larger lookup is walked again, so the
// walk's cancellation checks pace a result that shared arrays multiply.
const nameMemoMaxLen = 64

// nameOverNested continues nameOverArray from frame once an item of it is
// the array nested, keeping the frames of enclosing arrays on a stack. It
// skips an array open on the path, as in a value that contains itself.
// Once it has entered stripMemoAfter arrays, it memoizes the small lookups
// of the arrays it completes, since an array's lookup does not depend on
// where it is, and the path records every array, so a value that contains
// itself is cut where it first re-enters itself. It polls env, which may be
// nil, for cancellation, and under WithSequence checks the values found so
// far once two items have yielded some: the lookup is then a sequence the
// limit applies to, rather than a lone item's array a last step passes
// through, and shared arrays can multiply it far past the limit.
func nameOverNested(key string, frame nameFrame, nested []any, env *Environment) (nameFrame, error) {
	var (
		buf     [4]nameFrame
		path    valuePath
		done    map[sliceKey][]any
		entered int
		visits  int
	)
	found := newFoundCount(frame, env)
	stack := append(buf[:0], frame, newNameFrame(nested))
	path.enter(nested)
	for {
		top := &stack[len(stack)-1]
		if top.index == len(top.items) {
			if len(stack) == 1 {
				return *top, nil
			}
			path.leave(top.items)
			if done != nil && len(top.items) > 0 && len(top.seq.Values) <= nameMemoMaxLen {
				done[sliceKey{first: &top.items[0], n: len(top.items)}] = top.seq.Values
			}
			outer := &stack[len(stack)-2]
			outer.seq.Values = append(outer.seq.Values, top.seq.Values...)
			stack = stack[:len(stack)-1]
			continue
		}
		visits++
		if env != nil && visits%cancelCheckInterval == 0 {
			if err := env.Err(); err != nil {
				return nameFrame{}, err
			}
		}
		item := top.items[top.index]
		top.index++
		val, nested := nameLookup(key, item, true)
		switch {
		case nested == nil:
			before := len(top.seq.Values)
			top.add(val)
			if err := found.add(top, before); err != nil {
				return nameFrame{}, err
			}
			continue
		case len(nested) == 0:
			continue
		}
		if values, ok := done[sliceKey{first: &nested[0], n: len(nested)}]; ok {
			before := len(top.seq.Values)
			top.seq.Values = append(top.seq.Values, values...)
			if err := found.add(top, before); err != nil {
				return nameFrame{}, err
			}
			continue
		}
		if !path.enter(nested) {
			continue
		}
		stack = append(stack, newNameFrame(nested))
		entered++
		if entered == stripMemoAfter {
			done = make(map[sliceKey][]any)
			path.trackAll()
			for _, open := range stack {
				path.markOpen(open.items)
			}
		}
	}
}

// foundCount counts the values nameOverNested has found, and how many items
// yielded them, for the WithSequence check.
type foundCount struct {
	env     *Environment // nil when no limit applies
	values  int
	yielded int
}

// newFoundCount counts from the values frame holds, as from one item.
func newFoundCount(frame nameFrame, env *Environment) foundCount {
	if env == nil || !env.sequenceLimited {
		return foundCount{}
	}
	return foundCount{env: env, values: len(frame.seq.Values), yielded: min(len(frame.seq.Values), 1)}
}

// add counts the values top's last item appended to it, which held before
// of them, and checks the total once two items have yielded values.
func (c *foundCount) add(top *nameFrame, before int) error {
	added := len(top.seq.Values) - before
	if c.env == nil || added == 0 {
		return nil
	}
	c.values += added
	c.yielded++
	if c.yielded < 2 {
		return nil
	}
	return c.env.CheckSequence(c.values)
}

// newNameFrame starts mapping over items with a sequence like
// CreateSequence's, held in the frame so it need not be allocated.
func newNameFrame(items []any) nameFrame {
	return nameFrame{items: items, seq: Sequence{Values: make([]any, 0, 4)}}
}

// add adds the lookup's value for one item to the frame. Array results from
// navigating through arrays are flattened, except a constructed array, as
// jsonata-js evaluateStep does.
func (f *nameFrame) add(val any) {
	if val == nil {
		return
	}
	switch val.(type) {
	case []any, KeptArray, RawSequence:
		inner, _ := AsArray(val)
		for _, sv := range inner {
			f.seq.Values = append(f.seq.Values, NilAsNull(sv))
		}
		return
	}
	appendToSequence(&f.seq, val)
}

// collapse collapses the frame's sequence as jsonata-js collapses the
// sequence an expression returns; with keepArray (the [] suffix) a one-item
// sequence stays a KeptArray.
func (f *nameFrame) collapse(keepArray bool) any {
	switch len(f.seq.Values) {
	case 0:
		return nil
	case 1:
		if keepArray {
			return KeptArray{f.seq.Values[0]}
		}
		return f.seq.Values[0]
	}
	return CollapseSequence(&f.seq)
}

func fieldValue(val any) any {
	if val == nil {
		return Null
	}
	return flattenKept(val)
}

// evalWildcard evaluates * against one context item (see wildcardItem).
func evalWildcard(_ *parser.Node, input any, env *Environment) (any, error) {
	c := collector{env: env}
	c.wildcardItem(input)
	return c.wildcardResult()
}

// evalPathStepWildcard maps a * step over a sequence of context items. A root
// path's first step sees the root array as one item, as jsonata-js wraps it.
func evalPathStepWildcard(step *parser.Node, input any, env *Environment) (any, error) {
	items, ok := input.([]any)
	if ok && step.RootContext && isRootInput(items, env) {
		return evalWildcard(step, input, env)
	}
	if !ok || len(items) == 1 {
		if ok {
			input = items[0]
		}
		return evalWildcard(step, input, env)
	}
	// As jsonata-js evaluateStep does, a lone item result that is a plain
	// array (see collector.wildcardResult) is returned as is; otherwise the
	// item results are flattened into one sequence.
	c := collector{env: env}
	defined, lonePlain := 0, false
	for _, item := range items {
		start := len(c.values)
		c.fromArray = false
		c.wildcardItem(item)
		if c.err != nil {
			return nil, c.err
		}
		if len(c.values) > start || c.fromArray {
			defined++
			lonePlain = c.fromArray
		}
	}
	c.fromArray = defined == 1 && lonePlain
	// jsonata-js flattens the items' results into the step's sequence,
	// unless a last step passes a lone plain array through.
	if !c.fromArray {
		if err := c.env.CheckSequence(len(c.values)); err != nil {
			return nil, err
		}
	}
	return c.wildcardResult()
}

// unwrapRoot returns a child of env in which no array is the root input:
// what a step evaluates once against the root array jsonata-js wraps sees
// that array as unwrapped.
func unwrapRoot(env *Environment) *Environment {
	child := NewChildEnvironment(env)
	child.SetRootInput(nil)
	return child
}

// isRootInput reports whether items is the root input (see SetRootInput).
func isRootInput(items []any, env *Environment) bool {
	root, ok := env.Lookup(rootInputKey)
	if !ok {
		return false
	}
	rootItems, ok := root.([]any)
	return ok && len(rootItems) == len(items) && (len(items) == 0 || &rootItems[0] == &items[0])
}

// collector gathers the values * and ** yield. It enforces WithSequence as
// values arrive and checks for cancellation as it walks, so a large or
// deeply shared structure cannot run unbounded before the limits apply.
type collector struct {
	env       *Environment
	values    []any
	visits    int
	fromArray bool // some value came from flattening an array member
	err       error
}

// cancelCheckInterval is how many visited values pass between cancellation
// checks in a walk over a value (see collector and EachLeaf); checking every
// value would dominate a small walk.
const cancelCheckInterval = 1024

func (c *collector) visit() bool {
	if c.err != nil {
		return false
	}
	c.visits++
	if c.visits%cancelCheckInterval == 0 {
		c.err = c.env.Err()
	}
	return c.err == nil
}

// add collects v. Once * has appended an array value, jsonata-js's result
// is a plain array whose pushes are not counted against WithSequence.
func (c *collector) add(v any) {
	c.values = append(c.values, v)
	if !c.fromArray {
		c.err = c.env.CheckSequence(len(c.values))
	}
}

// wildcardItem collects what * yields for one item, as jsonata-js
// evaluateWildcard does: an object's values or an array's elements, with
// arrays among them flattened at every depth. A scalar yields nothing.
func (c *collector) wildcardItem(item any) {
	if arr, ok := AsArray(item); ok {
		for _, elem := range arr {
			c.wildcardValue(elem)
		}
		return
	}
	if IsMap(item) {
		MapRange(item, func(_ string, val any) bool {
			c.wildcardValue(val)
			return c.err == nil
		})
	}
}

// wildcardValue collects one value * yields. An array value is flattened
// and appended as jsonata-js's $append does, which counts the whole result.
func (c *collector) wildcardValue(val any) {
	c.flatten(val)
	if _, isArr := AsArray(val); isArr && c.err == nil {
		c.err = c.env.CheckSequence(len(c.values))
	}
}

// flatten collects v, or the items of an array v at every depth (see
// EachLeaf, which checks for cancellation).
func (c *collector) flatten(v any) {
	arr, ok := AsArray(v)
	if !ok {
		if c.visit() {
			c.add(NilAsNull(v))
		}
		return
	}
	if c.err != nil {
		return
	}
	c.fromArray = true
	err := EachLeaf("search", arr, -1, c.env, func(item any) error {
		c.add(NilAsNull(item))
		return c.err
	})
	if c.err == nil {
		c.err = err
	}
}

// wildcardResult returns the collected values. jsonata-js appends flattened
// array members with fn.append, which yields a plain array that no longer
// collapses to a single value or undefined.
func (c *collector) wildcardResult() (any, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.fromArray {
		if c.values == nil {
			return []any{}, nil
		}
		return c.values, nil
	}
	return CollapseSequence(&Sequence{Values: c.values}), nil
}

// descendants collects v and every value nested in it, as jsonata-js
// recurseDescendants does. Arrays are transparent: they contribute their
// members but are never values themselves, so a later step does not match
// both an array and its elements. It reports U1001 for a value that
// contains itself, which Go input can.
func (c *collector) descendants(v any) {
	if v == nil {
		return
	}
	// frame walks the children of self, an array or object.
	type frame struct {
		children []any
		self     any
	}
	var buf [8]frame
	path := valuePath{what: "search"}
	stack := append(buf[:0], frame{children: []any{v}})
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if len(top.children) == 0 {
			if len(stack) > 1 {
				path.leave(top.self)
			}
			stack = stack[:len(stack)-1]
			continue
		}
		v := NilAsNull(top.children[0])
		top.children = top.children[1:]
		if !c.visit() {
			return
		}
		if seq, ok := v.(*Sequence); ok {
			v = seq.Values
		}
		if arr, ok := AsArray(v); ok {
			if !path.enter(arr) {
				c.err = path.cycleError()
				return
			}
			stack = append(stack, frame{children: arr, self: arr})
			continue
		}
		c.add(v)
		if c.err == nil && IsMap(v) {
			if !path.enter(v) {
				c.err = path.cycleError()
				return
			}
			values := make([]any, 0, MapLen(v))
			MapRange(v, func(_ string, val any) bool {
				values = append(values, val)
				return true
			})
			stack = append(stack, frame{children: values, self: v})
		}
	}
}
