package parser_test

import (
	"strings"
	"testing"

	"github.com/recolabs/gnata/internal/parser"
)

// robustnessCorpus exercises every grammar construct so that truncating or
// corrupting it at each offset reaches the parser's error branches.
var robustnessCorpus = []string{
	`function($a, $b)<n-n:n>{ $a + $b }`,
	`λ($x){ $x * 2 }(3)`,
	`$ ~> |Account.Order|{"total": $sum(Product.Price)}, ["Product"]|`,
	`{"a": [1, 2.5e3, "s", true, false, null], "b": {"c": -1}}`,
	`Account.Order^(>Price, <Name).Product[Price > 10 and Qty != 2 or $not(x)]`,
	`library.loans@$l.books@$b[$l.isbn = $b.isbn]#$i.{"t": $b.title, "i": $i}`,
	`Account.Order.Product{%.OrderID: $sum(Price)}`,
	`$match(name, /^a[b-c]+$/i) ~> $count`,
	`($x := 1; $y := $x ? "yes" : "no"; $y ?? "none" ?: "empty")`,
	`$substring(?, 1, ?)("abc", 2)`,
	`[1..10][$ % 2 = 0] & "-" & **.Name & *.x & a.b[]`,
	`"str\"ing\u00e9" in ["a", 'b'] ~> $string()`,
	"`quoted name`.field",
	`/* comment */ a <= b >= c < d > e - f / g`,
}

func FuzzParse(f *testing.F) {
	for _, src := range robustnessCorpus {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		assertCleanParse(t, src)
	})
}

func TestParseRobustness(t *testing.T) {
	for _, src := range robustnessCorpus {
		for i := range len(src) + 1 {
			assertCleanParse(t, src[:i])
			assertCleanParse(t, src[:i]+`"`+src[i:])
			assertCleanParse(t, src[:i]+`)`+src[i:])
		}
	}
}

func assertCleanParse(t *testing.T, src string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("parse(%q) panicked: %v", src, r)
		}
	}()
	node, err := parser.NewParser(src).Parse()
	if err != nil {
		if !strings.Contains(err.Error(), "JSONata error S0") {
			t.Fatalf("parse(%q) error %q lacks a JSONata S0xxx code", src, err)
		}
		return
	}
	if _, err := parser.ProcessAST(node); err != nil {
		t.Fatalf("ProcessAST(%q) failed after a successful parse: %v", src, err)
	}
}

func TestProcessASTIsIdempotent(t *testing.T) {
	for _, src := range robustnessCorpus {
		node := mustParse(t, src)
		if _, err := parser.ProcessAST(node); err != nil {
			t.Fatalf("second ProcessAST(%q): %v", src, err)
		}
	}
	if node, err := parser.ProcessAST(nil); node != nil || err != nil {
		t.Fatalf("ProcessAST(nil) = %v, %v; want nil, nil", node, err)
	}
}

// signatureCases are ParseSig inputs, which exclude the surrounding <...>.
var signatureCases = []struct {
	sig  string
	code string
}{
	{sig: `n-n:n`},
	{sig: `a<n>?f<n:n>`},
	{sig: `(sn)+x?j`},
	{sig: `q`, code: "S0402"},
	{sig: `n<s>`, code: "S0401"},
	{sig: `a<n`, code: "S0402"},
	{sig: `(n`, code: "S0402"},
	{sig: `(a<n>)`, code: "S0402"},
	{sig: `(nq)`, code: "S0402"},
}

func TestParseSig(t *testing.T) {
	for _, tC := range signatureCases {
		t.Run(tC.sig, func(t *testing.T) {
			_, err := parser.ParseSig(tC.sig)
			if tC.code == "" {
				if err != nil {
					t.Fatalf("ParseSig(%q): %v", tC.sig, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tC.code) {
				t.Fatalf("ParseSig(%q) error = %v, want %s", tC.sig, err, tC.code)
			}
		})
	}
}
