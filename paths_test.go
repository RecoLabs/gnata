package gnata_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/recolabs/gnata"
)

const (
	accountJSON = `{"Account":{"Name":"Firefly","Order":[` +
		`{"OrderID":"o1","Product":[{"Name":"Hat","Price":10,"Qty":2},{"Name":"Cap","Price":5,"Qty":1}]},` +
		`{"OrderID":"o2","Product":[{"Name":"Bag","Price":20,"Qty":1}]}]}}`
	// A root array is one context, so it is the parent of the items it
	// yields.
	rootArrayJSON  = `[{"k":[1,2],"m":[3]},{"k":[4],"m":[]}]`
	nestedJoinJSON = `{"a":{"b":[{"c":[{"d":1},{"d":2}],"k":"x"},{"c":[{"d":3}],"k":"y"}]}}`
	libraryJSON    = `{"library":{"books":[{"title":"A","isbn":"1"},{"title":"B","isbn":"2"}],` +
		`"loans":[{"isbn":"1","customer":"c1"},{"isbn":"2","customer":"c2"},{"isbn":"1","customer":"c3"}]}}`
	nestedJSON = `{"x":[1,2],"y":[[1,2],[3]],"z":[{"a":1},2,[3,[4]]],"m":{"a":[1,[2]],"b":{"c":3}}}`
	tripleJSON = `{"a":[{"b":1,"c":2},{"b":3,"c":4},{"b":2,"c":9}]}`
	// parentArrayJSON is a root array, which jsonata-js wraps as one item.
	parentArrayJSON = `[{"k":[1,2],"m":[1]},{"k":4,"m":[2,3]}]`
	keyedJSON       = `{"a":[{"b":"k","c":[1,2]},{"b":"k","c":[3]},{"b":"j","c":4}],"e":[]}`
	rootItemsJSON   = `[{"k":[{"v":1},{"v":2}],"n":"p"},{"k":[{"v":3}],"n":"q"}]`
	nestedItemsJSON = `{"x":[[1,2],[3]],"y":[[{"n":"a"},{"n":"b"}],[{"n":"c"}]],"n":"top"}`
	levelsJSON      = `{"a":{"y":2,"b":[{"x":5,"y":6,"c":[{"x":7},{"x":8}],"z":true},{"x":9,"y":10,"c":{"x":11},"z":false}]}}`
	rootPairsJSON   = `[{"n":"p","v":1},{"n":"q","v":2}]`
)

var parentOperatorCases = []exprCase{
	{
		expr: `Account.Order.Product.{"n": Name, "o": %.OrderID}`, data: accountJSON,
		want: `[{"n":"Hat","o":"o1"},{"n":"Cap","o":"o1"},{"n":"Bag","o":"o2"}]`,
	},
	{expr: `Account.Order.Product[%.OrderID="o2"].Name`, data: accountJSON, want: `"Bag"`},
	{expr: `Account.Order.Product.%.%.Name`, data: accountJSON, want: `["Firefly","Firefly","Firefly"]`},
	{expr: `(Account.Order.Product)[%.OrderID="o1"].Name`, data: accountJSON, want: `["Hat","Cap"]`},
	{expr: `Account.(Order.Product).{"o": %.OrderID}`, data: accountJSON, want: `[{"o":"o1"},{"o":"o1"},{"o":"o2"}]`},
	{expr: `Account.Order.Product{%.OrderID: $sum(Price)}`, data: accountJSON, want: `{}`},
	{expr: `Account.Order.Product^(%.OrderID, Price).Name`, data: accountJSON, want: `["Cap","Hat","Bag"]`},
	{
		expr: `library.loans@$l.{"c":$l.customer, "p": %.books[0].title}`, data: libraryJSON,
		want: `[{"c":"c1","p":"A"},{"c":"c2","p":"A"},{"c":"c3","p":"A"}]`,
	},
	{expr: `Account.Order.Product.%[0].OrderID`, data: accountJSON, want: `"o1"`},
	{expr: `Account.Order.Product.%[-1].OrderID`, data: accountJSON, want: `"o2"`},
	{
		expr: `Account.Order.Product.%@$o.{"n":Name,"o":$o.OrderID}`, data: accountJSON,
		want: `[{"n":"Hat","o":"o1"},{"n":"Cap","o":"o1"},{"n":"Bag","o":"o2"}]`,
	},
	{expr: `Account.Order.Product.%[1]#$i.{"i":$i,"o":OrderID}`, data: accountJSON, want: `{"i":0,"o":"o1"}`},
	{expr: `Account.Order.Product.%#$i.$i`, data: accountJSON, want: `[0,0,0]`},
	{expr: `k@$a.m@$b.%.m`, data: rootArrayJSON, want: `[3,3,3]`},
	{expr: `a.%`, data: `{"a":{"b":1}}`, want: `{"a":{"b":1}}`},
	{expr: `$count(k.%)`, data: parentArrayJSON, want: `6`},
	{expr: `(function(){ $count(k.%) })()`, data: parentArrayJSON, want: `6`},
	{expr: `$count((function(){ *.% })())`, data: parentArrayJSON, want: `4`},
	{expr: `$count((k.%))`, data: parentArrayJSON, want: `6`},
	{expr: `$count((k).%)`, data: parentArrayJSON, want: `3`},
	{expr: `$count($$.k.%)`, data: parentArrayJSON, want: `3`},
	{expr: `$count(k[0].%)`, data: parentArrayJSON, want: `2`},
	{expr: `$count(*.%)`, data: parentArrayJSON, want: `4`},
	{expr: `(k.%).k`, data: parentArrayJSON, want: `[1,2,1,2,4]`},
	{expr: `k.%#$i.$i`, data: parentArrayJSON, want: `[0,1,0,1,0,1]`},
	{expr: `(k).%#$i.$i`, data: parentArrayJSON, want: `[0,0,0]`},
	{expr: `k#$i.%.$i`, data: parentArrayJSON, want: `[0,0,1,1,2,2]`},
	{expr: `k@$a.%.$a`, data: parentArrayJSON, want: `[1,1,2,2,4,4]`},
	{expr: `Account.Order.Product.$sum(%.Product.Price)`, data: accountJSON, want: `[15,15,20]`},
	{expr: `Account.Order.Product.Name.$string(%.Price)`, data: accountJSON, want: `["10","5","20"]`},
	{expr: `Account.Order.Product.Name[0].%.Price`, data: accountJSON, want: `10`},
	{expr: `Account.Order.Product[0][%.OrderID="o2"].Name`, data: accountJSON, want: undefined},
	{expr: `Account.Order.Product.(%.Product)[0].Name`, data: accountJSON, want: `"Hat"`},
	{expr: `Account.Order.(Product)[0].%.OrderID`, data: accountJSON, want: `"o1"`},
	{expr: `Account.Order.Product.((Name).%).%.OrderID`, data: accountJSON, want: `["o1","o1","o2"]`},
	{expr: `Account.Order.Product.Name.(%.%).OrderID`, data: accountJSON, want: `["o1","o1","o2"]`},
	{
		expr: `Account.Order#$o.(Product#$i).%.{"o":$o,"i":$i}`, data: accountJSON,
		want: `[{"i":0,"o":0},{"i":1,"o":0},{"i":0,"o":1}]`,
	},
	{expr: `Account.Order#$o.(Product#$i).{"o":$o,"i":$i}`, data: accountJSON, want: `[{"o":0},{"o":0},{"o":1}]`},
	{expr: `Account.Order.($x := 1; Product).%.{"x":$x}`, data: accountJSON, want: `[{},{},{}]`},
	{expr: `Account.Order.(Product{Name:Price}).%.OrderID`, data: accountJSON, want: undefined},
	{
		expr: `Account.Order.Product@$p.%.{"o":OrderID,"n":$p.Name}`, data: accountJSON,
		want: `[{"n":"Hat","o":"o1"},{"n":"Cap","o":"o1"},{"n":"Bag","o":"o2"}]`,
	},
	{
		expr: `Account.Order.Product#$i.%.{"o":OrderID,"i":$i}`, data: accountJSON,
		want: `[{"i":0,"o":"o1"},{"i":1,"o":"o1"},{"i":0,"o":"o2"}]`,
	},
	{expr: `Account.Order@$o.Product@$p.%.OrderID`, data: accountJSON, want: undefined},
	{expr: `Account.Order.Product^(%.OrderID)[Name="Hat"]`, data: accountJSON, want: undefined},
	{expr: `Account.Order.Product^(%.OrderID)[$keys($)[1]="!0"].Name`, data: accountJSON, want: `["Hat","Cap","Bag"]`},
	{expr: `Account.Order.Product^(%.OrderID & (%.OrderID & %.OrderID)){"k":$keys($)}`, data: accountJSON, want: `{"k":["@","!0"]}`},
	{expr: `Account.Order.(Product#$i)^(>%.OrderID){"k":$keys($)}`, data: accountJSON, want: `{"k":["@","i","!0"]}`},
	{expr: `Account.Order{OrderID: Product.%.OrderID}`, data: accountJSON, want: `{"o1":["o1","o1"],"o2":"o2"}`},
	{expr: `Account.Order.Product{Name: $count(%)}`, data: accountJSON, want: `{"Bag":0,"Cap":0,"Hat":0}`},
	{expr: `Account[%]`, data: accountJSON, want: undefined},
	{expr: `Account.(*).%`, data: accountJSON, want: undefined},
	{expr: `Account.Order.Product.$map([1], function($v){%})`, data: accountJSON, want: undefined},
	{expr: `Account.Order.Product.(%.OrderID ~> $uppercase())`, data: accountJSON, want: undefined},
	{expr: `Account.Order.(Product).%.OrderID`, want: undefined},
	{expr: `x."b"[0].%.c`, data: `{"x":{"b":[{"c":1},{"c":2}],"c":9}}`, want: `9`},
	{expr: `x."b"[c>1]#$i.$i`, data: `{"x":{"b":[{"c":1},{"c":2}],"c":9}}`, want: `0`},
	{expr: `$count("k"[0].%)`, data: parentArrayJSON, want: `2`},
	{expr: `a.[{"x":b},{"x":c}].(x).%`, data: pairsJSON, want: `[{"x":1},{"x":2},{"x":3},{"x":4}]`},
	{expr: `a.().%`, data: pairsJSON, want: undefined},
	{expr: `a.($error("boom"); b).%`, data: pairsJSON, code: "D3137"},
	{expr: `a.b.c.((%.x)[%.%.z])`, data: levelsJSON, want: `[5,5]`},
	{expr: `a.b.c.((%.x; %.%.y)[%.%.z])`, data: levelsJSON, want: `[2,2]`},
	{expr: `library.loans@$l.%.$l.customer`, data: libraryJSON, want: `["c1","c2","c3"]`},
	{expr: `a.b.%@$p[0].{"p":$p.c}`, data: `{"a":[{"b":[1,2],"c":"x"},{"b":[3],"c":"y"}]}`, want: `{"p":"x"}`},
	{expr: `a.b.%@$p[$p.c="y"].{"p":$p.c}`, data: `{"a":[{"b":[1,2],"c":"x"},{"b":[3],"c":"y"}]}`, want: `{"p":"y"}`},
	{expr: `a.b.%@$p[c="y"]`, data: `{"a":[{"b":[1,2],"c":"x"},{"b":[3],"c":"y"}]}`, want: undefined},
	{expr: `library.(loans@$l).%.$l.customer`, data: libraryJSON, want: `["c1","c2","c3"]`},
	{
		expr: `library.loans#$i.(%)#$j.{"i":$i,"j":$j}`, data: libraryJSON,
		want: `[{"i":0,"j":0},{"i":1,"j":0},{"i":2,"j":0}]`,
	},
}

// staticParentCases are % operators whose step jsonata-js cannot derive,
// which Compile rejects with S0217 before any input is seen.
var staticParentCases = []string{
	`%`,
	`%.a`,
	`a.b.%.%.%.x`,
	`$string(%)`,
	`Account.Order.Product^(Price).%.OrderID`,
	`Account.Order.(Product^(Price)).%`,
	`Account.Order.Product.$.%`,
	`Account.Order.**.%`,
	`a.[b,c].%`,
	`k#$j.%.%`,
	`(Account)[%.x]`,
	`*[%.x]`,
	`a."b"[%.x]`,
	`a.$^(%.b)`,
}

func TestStaticParentErrors(t *testing.T) {
	for _, expr := range staticParentCases {
		t.Run(expr, func(t *testing.T) {
			if _, err := gnata.Compile(expr); err == nil || !strings.Contains(err.Error(), "S0217") {
				t.Fatalf("Compile(%s): want S0217, got %v", expr, err)
			}
		})
	}
}

var bindingOperatorCases = []exprCase{
	{expr: `a#$j{"k":$j}`, data: pairsJSON, want: `{"k":[0,1]}`},
	{expr: `o#$j{"k":$j}`, data: pairsJSON, want: `{"k":0}`},
	{expr: `a@$v{"k":$v.b}`, data: pairsJSON, want: `{"k":[1,3]}`},
	{expr: `a^(>b)#$i{"k":$i}`, data: pairsJSON, want: `{"k":[0,1]}`},
	{expr: `a[0]#$i{"k":$i}`, data: pairsJSON, want: `{"k":0}`},
	{expr: `a#$i{"k":$i}[0]`, data: pairsJSON, want: `{"k":0}`},
	{expr: `(a)#$i{"k":$i}[0]`, data: pairsJSON, want: `{"k":0}`},
	{expr: `$map([1], $keys)#$i`, want: undefined},
	{expr: `$map([1], $keys)#$i{"x":$i}`, want: `{}`},
	{expr: `a@$v[$v.b>1].$v.c`, data: pairsJSON, want: `4`},
	{expr: `$.(a#$i.$i)`, data: `[[{"a":[1,2]},{"a":[3]}],[{"a":[4]}]]`, want: `[0,1,0,0]`},
	{expr: `$count($)#$i`, data: rootItemsJSON, want: `2`},
	{expr: `{"a":$count($)}#$i`, data: rootItemsJSON, want: `{"a":2}`},
	{expr: `-$count($)#$i`, data: rootItemsJSON, want: `-2`},
	{expr: `$count($)@$v.$v`, data: rootItemsJSON, want: `2`},
	{expr: `x.$count($)#$i`, data: nestedItemsJSON, want: `[2,1]`},
	{expr: `oo#$i.{a:b}`, data: `{"oo":[[{"a":"p","b":1},{"a":"q","b":2}],[{"a":"p","b":3}]]}`, want: `[{"p":1,"q":2},{"p":3}]`},
	{expr: `oo.{a:b}`, data: `{"oo":[[{"a":"p","b":1},{"a":"q","b":2}],[{"a":"p","b":3}]]}`, want: `[{"p":1,"q":2},{"p":3}]`},
	{expr: `{n:1}#$i`, data: `[{"n":"p"},{"n":"q"}]`, want: `{"p":1,"q":1}`},
	{expr: `{n:1}`, data: `[{"n":"p"},{"n":"q"}]`, code: "T1003"},
	{expr: `x.{"k":$count($)}`, data: nestedItemsJSON, want: `[{"k":2},{"k":1}]`},
	{expr: `y.(%.n)`, data: nestedItemsJSON, want: `["top","top","top"]`},
	{expr: `y.{"a":%.n}`, data: nestedItemsJSON, want: `[{"a":["top","top"]},{"a":"top"}]`},
	{expr: `a#$i.$i`, data: `[[{"a":[1,2]},{"a":[3]}],[{"a":[4]}]]`, want: `[0,1,2,3]`},
	{expr: `$^($)#$i.{"v":$,"i":$i}`, data: `[3,1,4]`, want: `[{"i":0,"v":1},{"i":1,"v":3},{"i":2,"v":4}]`},
	{expr: `$^(>$).($)#$j.$j`, data: `[3,1,4]`, want: `[0,0,0]`},
	{expr: `($v := [3,1]; $v^($)#$i.{"v":$,"i":$i})`, want: `[{"i":0,"v":1},{"i":1,"v":3}]`},
	{
		expr: `Account.Order#$i.Product.{"i":$i, "n":Name}`, data: accountJSON,
		want: `[{"i":0,"n":"Hat"},{"i":0,"n":"Cap"},{"i":1,"n":"Bag"}]`,
	},
	{expr: `Account.Order#$i.Product^(>Price).Name`, data: accountJSON, want: `["Bag","Hat","Cap"]`},
	{
		expr: `library.loans@$l.books@$b[$l.isbn=$b.isbn].{"title":$b.title,"customer":$l.customer}`, data: libraryJSON,
		want: `[{"customer":"c1","title":"A"},{"customer":"c2","title":"B"},{"customer":"c3","title":"A"}]`,
	},
	{
		expr: `library.loans@$l.books@$b[$l.isbn=$b.isbn][0].{"title":$b.title,"customer":$l.customer}`, data: libraryJSON,
		want: `{"customer":"c1","title":"A"}`,
	},
	{
		expr: `library.loans@$l.books@$b[$l.isbn=$b.isbn][-1].{"title":$b.title,"customer":$l.customer}`, data: libraryJSON,
		want: `{"customer":"c3","title":"A"}`,
	},
	{
		expr: `library.loans@$l.books@$b#$i[$l.isbn=$b.isbn].{"title":$b.title,"i":$i}`, data: libraryJSON,
		want: `[{"i":0,"title":"A"},{"i":1,"title":"B"},{"i":0,"title":"A"}]`,
	},
	{
		expr: `library.loans@$l.books@$b[$l.isbn=$b.isbn]#$j.{"title":$b.title,"j":$j}`, data: libraryJSON,
		want: `[{"j":0,"title":"A"},{"j":1,"title":"B"},{"j":2,"title":"A"}]`,
	},
	{expr: `library.loans@$l.books@$b[$l.isbn=$b.isbn][$error("p")]`, data: libraryJSON, code: "D3137"},
	{expr: `library.loans@$l.books@$b[$error("p")]`, data: libraryJSON, code: "D3137"},
	{expr: `a#$i[$i>0]`, data: `{"a":[1,2,3]}`, want: `[2,3]`},
	{expr: `a#$i.$i`, data: `{"a":[1,2,3]}`, want: `[0,1,2]`},
	{expr: `a@$x.$x`, data: `{"a":[1,2,3]}`, want: `[1,2,3]`},
	{expr: `a@$x#$i.{"x":$x,"i":$i}`, data: `{"a":[1,2,3]}`, want: `[{"i":0,"x":1},{"i":1,"x":2},{"i":2,"x":3}]`},
	{expr: `(a)#$i.{"v":$,"i":$i}`, data: `{"a":[1,2,3]}`, want: `[{"i":0,"v":1},{"i":1,"v":2},{"i":2,"v":3}]`},
	{expr: `a.b#$i.{"v":$,"i":$i}`, data: `{"a":[{"b":[1,2]},{"b":3}]}`, want: `[{"i":0,"v":1},{"i":1,"v":2},{"i":0,"v":3}]`},
	{expr: `a.b#$i[$i=1]`, data: `{"a":[{"b":[1,2]},{"b":3}]}`, want: `2`},
	{expr: `a#$i^($)[0]`, data: `{"a":[3,1,2]}`, want: `1`},
	{expr: `Account.Order.Product#$i{Name: $i}`, data: accountJSON, want: `{"Bag":0,"Cap":1,"Hat":0}`},
	{expr: `Account.Order.Product#$i{$i: Name}`, data: accountJSON, code: "T1003"},
	{expr: `Account.Order#$i.Product{Name: $error("g")}`, data: accountJSON, code: "D3137"},
	{expr: `Account.Order.Product@$p{$p.Name: $p.Price}`, data: accountJSON, want: `{"Bag":20,"Cap":5,"Hat":10}`},
	{expr: `Account.Order@$o.$o.Product{%.OrderID: Name}`, data: accountJSON, want: `{}`},
	{
		expr: `Account.Order.Product[Price>5]#$i.{"n":Name,"i":$i}`, data: accountJSON,
		want: `[{"i":0,"n":"Hat"},{"i":1,"n":"Bag"}]`,
	},
	{
		expr: `Account.Order.Product#$i[Price>5].{"n":Name,"i":$i}`, data: accountJSON,
		want: `[{"i":0,"n":"Hat"},{"i":0,"n":"Bag"}]`,
	},
	{expr: `Account.Order.Product[Price>5]#$i[$i=1].Name`, data: accountJSON, want: `"Bag"`},
	{expr: `Account.Order.Product[0]#$i.Name`, data: accountJSON, want: `"Hat"`},
	{expr: `Account.Order.Product#$i[-1].Name`, data: accountJSON, want: `"Bag"`},
	{expr: `Account.Order.Product#$i[[0,2]].Name`, data: accountJSON, want: `["Hat","Bag"]`},
	{expr: `Account.Order#$o.Product[0].Name`, data: accountJSON, want: `"Hat"`},
	{expr: `Account.Order#$o.(Product)[0].Name`, data: accountJSON, want: `"Hat"`},
	{expr: `Account.Order#$o.Product.(Name)[1]`, data: accountJSON, want: `"Cap"`},
	{expr: `Account.Order#$o.$string(OrderID)[-1]`, data: accountJSON, want: `"o2"`},
	{expr: `Account.Order#$o.[OrderID, $o][1]`, data: accountJSON, want: `0`},
	{expr: `($v := [10, 20]; Account.Order#$o.$v[1])`, data: accountJSON, want: `20`},
	{expr: `Account.Order#$o.$[1].OrderID`, data: accountJSON, want: `"o2"`},
	{expr: `Account.Order#$o.Product.$uppercase(Name)[1]`, data: accountJSON, want: `"CAP"`},
	{expr: `Account.Order#$o.**[-1]`, data: accountJSON, want: `1`},
	{expr: `Account.Order#$o.{"k":$o}[1]`, data: accountJSON, want: `{"k":1}`},
	{expr: `Account.Order#$o.-$count(Product)[0]`, data: accountJSON, want: `[-2,-1]`},
	{
		expr: `Account.Order#$o.(Product)[0]#$p.{"n":Name,"o":$o,"p":$p}`, data: accountJSON,
		want: `{"n":"Hat","o":0,"p":0}`,
	},
	{expr: `Account.Order.Product[0].Name`, data: accountJSON, want: `["Hat","Bag"]`},
	{
		expr: `Account.Order.Product^(Price)#$i.{"n":Name,"i":$i}`, data: accountJSON,
		want: `[{"i":0,"n":"Cap"},{"i":1,"n":"Hat"},{"i":2,"n":"Bag"}]`,
	},
	{expr: `[3,1,2]^($)#$i.{"v":$,"i":$i}`, want: `[{"i":0,"v":1},{"i":1,"v":2},{"i":2,"v":3}]`},
	{expr: `Account.Order#$o.Product^(Price)#$i.$i`, data: accountJSON, want: undefined},
	{expr: `Account.Order.(Product)[%.OrderID="o2"].Name`, data: accountJSON, want: `"Bag"`},
	{expr: `Account.Order.(Product)[Price>5][%.OrderID="o1"].Name`, data: accountJSON, want: `"Hat"`},
	{expr: `Account.Order#$i.Product[$i=1].Name`, data: accountJSON, want: `"Bag"`},
	{expr: `(Account.Order.Product)^(%.OrderID).Name`, data: accountJSON, want: `["Hat","Cap","Bag"]`},
	{expr: `a.(b)@$v.$v`, data: `{"a":[{"b":[1,2],"c":"x"},{"b":[3],"c":"y"}]}`, want: `[1,2,3]`},
	{expr: `a.(b)@$v.c`, data: `{"a":[{"b":[1,2],"c":"x"},{"b":[3],"c":"y"}]}`, want: `["x","x","y"]`},
	{expr: `a.(b)#$i.$i`, data: `{"a":[{"b":[1,2],"c":"x"},{"b":[3],"c":"y"}]}`, want: `[0,1,0]`},
	{expr: `a.[b,c].$sum($)#$i`, data: pairsJSON, want: `[3,7]`},
	{expr: `a.[b,c].$sum($)#$i.{"v":$,"i":$i}`, data: pairsJSON, want: `[{"i":0,"v":3},{"i":0,"v":7}]`},
	{expr: `a.[b,c].{"k":$}@$o.$o`, data: pairsJSON, want: `[{"k":[1,2]},{"k":[3,4]}]`},
	{expr: `a.[b,c].*#$i`, data: pairsJSON, want: `[1,2,3,4]`},
	{expr: `a.[b,c].*#$i.$i`, data: pairsJSON, want: `[0,1,0,1]`},
	{expr: `a.[b,c].$#$i.$i`, data: pairsJSON, want: `[0,1,0,1]`},
	{expr: `a.[b,c].$string()#$i`, data: pairsJSON, want: `["[1,2]","[3,4]"]`},
	{expr: `a#$j.[b,c].$string()`, data: pairsJSON, want: `["1","2","3","4"]`},
	{expr: `a.[b,c]^(>$[0])#$i.$i`, data: pairsJSON, want: `[0,1]`},
	{expr: `a.[b,c]^(>$[0])#$i.{"v":$,"i":$i}`, data: pairsJSON, want: `[{"i":0,"v":[3,4]},{"i":1,"v":[1,2]}]`},
	{expr: `o.[b,c]^(>$[0])#$i.$i`, data: pairsJSON, want: `0`},
	{expr: `a.[b,c]^(>$[0])[0]#$i.$i`, data: pairsJSON, want: `0`},
	{expr: `a.[b,c]^(>$[0])[$[0]>1]#$i.$i`, data: pairsJSON, want: `0`},
	{expr: `a.[b,c]^(>$[0]).*#$i.$i`, data: pairsJSON, want: `[0,1,0,1]`},
	{expr: `a.[b,c]^(>$[0])[0].$string()#$i`, data: pairsJSON, want: `["3","4"]`},
	{expr: `a.[b,c]^(>$[0])[0].$#$i.$i`, data: pairsJSON, want: `[0,0]`},
	{expr: `o.[b,c]^(>$[0])[0].$string()#$i`, data: pairsJSON, want: `["5","6"]`},
	{expr: `a.[b,c]^(>$[0])[$[0]>1].$string()#$i`, data: pairsJSON, want: `"[3,4]"`},
	{expr: `a.[b,c]^(>$[0])[0][1].$string()#$i`, data: pairsJSON, want: `"4"`},
	{expr: `a.[b,c]^(>$[0])[0][$>3].$string()#$i`, data: pairsJSON, want: `"4"`},
	{expr: `n^($[0])[0].$count($)#$i.$i`, data: `{"n":[[2,1],[3,4]]}`, want: `[0,0]`},
	{expr: `a#$j^(>b)[0].$j`, data: tripleJSON, want: `1`},
	{expr: `a#$j^(>b)[[0,1]].{"j":$j,"c":c}`, data: tripleJSON, want: `[{"c":4,"j":1},{"c":9,"j":2}]`},
	{expr: `a#$j.b^(>$)[0].$j`, data: tripleJSON, want: `1`},
	{expr: `a#$j^(>b)[0]#$k.{"j":$j,"k":$k}`, data: tripleJSON, want: `{"j":1,"k":0}`},
	{expr: `a#$j^(>b)[$j=0].b`, data: tripleJSON, want: undefined},
	{expr: `a#$j^(>b)[$.j=1].c`, data: tripleJSON, want: `4`},
	{expr: `a#$j.b^(>$)[$>1]`, data: tripleJSON, code: "T2010"},
	{expr: `a#$j.b^(>$)[0][]`, data: tripleJSON, want: `[3]`},
	{expr: `a@$x^(>$x.b)[0].c`, data: tripleJSON, want: undefined},
	{expr: `a.[b,c].$sum($)#$i^(>$).{"i":$i,"v":$}`, data: pairsJSON, want: `[{"i":0,"v":7},{"i":0,"v":3}]`},
	{expr: `a#$j.[b,c]^(>$[0])[0].{"j":$j,"v":$}`, data: pairsJSON, want: `{"j":1,"v":4}`},
	{expr: `a#$j^(>b)^(c).$j`, data: tripleJSON, want: `[1,2,0]`},
	{expr: `a#$j.b^(>$)^($).$j`, data: tripleJSON, code: "T2008"},
	{expr: `a^(>b)#$i^(c).$i`, data: tripleJSON, want: `[2,0,1]`},
	{expr: `a#$j^(>b).c^($).$j`, data: tripleJSON, want: `[0,1,2]`},
	{expr: `a#$j^(>b)[0]{"k":$j}`, data: tripleJSON, want: `{}`},
	{expr: `a#$j^(>b)[0]{"k":j}`, data: tripleJSON, want: `{"k":1}`},
	{expr: `a#$j^(>b)[[0,1]]^(>c).$j`, data: tripleJSON, want: `[1,2]`},
	{expr: `a#$j^(>b)[]^(>c)[0].$j`, data: tripleJSON, want: `[1]`},
	{expr: `a#$j^(>b)^(>c)`, data: tripleJSON, want: `[{"b":3,"c":4},{"b":2,"c":9},{"b":1,"c":2}]`},
	{expr: `a#$j^(>b){"k":j}`, data: tripleJSON, want: `{"k":[1,2,0]}`},
	{expr: `a@$x#$j^(>$x.b)[$keys($)[2]="j"].$j`, data: tripleJSON, want: `[1,2,0]`},
	{expr: `a[]^(b).c`, data: tripleJSON, want: `[2,9,4]`},
	{expr: `Account.Order.Product#$i[true][0].Name`, data: accountJSON, want: `"Hat"`},
	{expr: `Account.Order.Product#$i[Price>5][-1].Name`, data: accountJSON, want: `"Bag"`},
	{expr: `Account.Order.Product#$i[Price<20][$i=0].Name`, data: accountJSON, want: `"Hat"`},
	{expr: `Account.Order.Product#$i[true][[0,2]].Name`, data: accountJSON, want: `["Hat","Bag"]`},
	{expr: `Account.Order.Product#$i[true][true][1].{"n":Name,"i":$i}`, data: accountJSON, want: `{"i":1,"n":"Cap"}`},
	{expr: `Account.Order.(Product)#$i[true][0].Name`, data: accountJSON, want: `"Hat"`},
	{expr: `Account.Order.Product@$p[0].$p.Name`, data: accountJSON, want: `"Hat"`},
	{expr: `Account.Order.Product@$p[$p.Price>5][0].$p.Name`, data: accountJSON, want: `"Hat"`},
	{expr: `Account.Order.Product@$p[OrderID="o2"].$p.Name`, data: accountJSON, want: `"Bag"`},
	{
		expr: `library.loans@$l.books@$b[$l.isbn=$b.isbn][$l.customer!="c1"][0].$b.title`, data: libraryJSON,
		want: `"B"`,
	},
	{expr: `library.loans@$l.books@$b[$l.isbn=$b.isbn][[0,2]].$l.customer`, data: libraryJSON, want: `["c1","c3"]`},
	{expr: `Account.Order.(Product[%.OrderID="o1"])@$p[$p.Price>5].$p.Name`, data: accountJSON, want: `"Hat"`},
	{expr: `Account.Order.(Product.%)@$o[$o.OrderID="o2"].$o.OrderID`, data: accountJSON, want: `"o2"`},
	{expr: `g#$i{k:$i}`, data: `{"g":[{"k":"x","v":[1,2]},{"k":"x","v":[3]},{"k":"y","v":1}]}`, want: `{"x":[0,1],"y":2}`},
	{expr: `g@$e{$e.k:$e.v}`, data: `{"g":[{"k":"x","v":[1,2]},{"k":"x","v":[3]},{"k":"y","v":1}]}`, want: `{"x":[1,2,3],"y":1}`},
	// @ on anything but a field name keeps its operand's value.
	{expr: `[1,2]@$v`, want: `[1,2]`},
	{expr: `$sum([1,2,3]@$i)`, want: `6`},
	{expr: `(n)@$e`, data: `{"n":[3,1,2]}`, want: `[3,1,2]`},
	// Group values merge the tuples' variables as $append does.
	{expr: `Account.Order[0]#$o.Product{"k":$o}`, data: accountJSON, want: `{"k":[0,0]}`},
	{expr: `b@$e{"x":$e}`, data: `{"b":[[1,2,3],[4,5]]}`, want: `{"x":[1,2,3,4,5]}`},
	// A constructed array is appended into an enclosing constructor.
	{expr: `[a.[b,c], 1]`, data: `{"a":{"b":1,"c":2}}`, want: `[1,2,1]`},
	{expr: `($c := a.[b,c]; [$c, 3])`, data: `{"a":{"b":1,"c":2}}`, want: `[1,2,3]`},
	{expr: `[[[1,2]][0], 3]`, want: `[[1,2],3]`},
	{expr: `[[[1,2]][[0,1]], 3]`, want: `[[1,2],3]`},
	{expr: `($o := {"a": $x := 1}; $x)`, want: `1`},
	// A tuple group skips a record whose key is undefined.
	{expr: `a#$i{k:$i}`, data: `{"a":[{"x":1,"k":"p"},{"x":2}]}`, want: `{"p":0}`},
	{expr: `r.a#$i{k:$i}`, data: `{"r":{"a":[{"x":1,"k":"p"},{"x":2}]}}`, want: `{"p":0}`},
	{expr: `a@$e{$e.k:$e.x}`, data: `{"a":[{"x":1,"k":"p"},{"x":2}]}`, want: `{"p":1}`},
	{expr: `$#$i{"k":$i}`, data: `[{"a":1},{"c":1}]`, want: `{"k":[0,1]}`},
	{expr: `a[[0]]#$i[]`, data: `{"a":[[1,2],[3,4]]}`, want: `[[1,2]]`},
}

var groupAndSortCases = []exprCase{
	{expr: `Account.Order.Product{Name: Price}`, data: accountJSON, want: `{"Bag":20,"Cap":5,"Hat":10}`},
	{expr: `Account.Order.{OrderID: Product.Name}`, data: accountJSON, want: `[{"o1":["Hat","Cap"]},{"o2":"Bag"}]`},
	{expr: `a{b: c}.k`, data: `{"a":[{"b":"k","c":1},{"b":"k","c":2}]}`, want: `{}`},
	{expr: `(a{b: c}).k`, data: `{"a":[{"b":"k","c":1},{"b":"k","c":2}]}`, want: `[1,2]`},
	{expr: `a.b{"k":$}.c`, data: pairsJSON, want: `{}`},
	{expr: `(a){"k":$}.b`, data: pairsJSON, want: undefined},
	{expr: `*{"k":$}.b`, data: pairsJSON, want: undefined},
	{expr: `*{"k":$}#$i.b`, data: pairsJSON, want: undefined},
	{expr: `a{"k":b}^($)`, data: pairsJSON, code: "T2008"},
	{expr: `a{"k":c}^(>b)`, data: pairsJSON, want: `{"k":[4,2]}`},
	{expr: `a{"k":b}^(c){"j":c}`, data: pairsJSON, code: "S0210"},
	{expr: `a.b{"k":$}{"j":$}`, data: pairsJSON, code: "S0210"},
	{expr: `a{"k":c}[0]`, data: pairsJSON, want: `{"k":2}`},
	{expr: `a{"k":c}[0][0]`, data: pairsJSON, want: `{"k":2}`},
	{expr: `a{"k":c}[0]{"j":1}`, data: pairsJSON, code: "S0210"},
	{expr: `a^(b){"k":c}[1]`, data: pairsJSON, want: `{"k":4}`},
	{expr: `a.b{"k":$}[0]`, data: pairsJSON, want: `{"k":[1,3]}`},
	{expr: `a.b{"k":$}[1]`, data: pairsJSON, want: `{}`},
	{expr: `(a){"k":c}[0]`, data: pairsJSON, code: "S0209"},
	{expr: `*{"k":$}#$i[0]`, data: pairsJSON, code: "S0209"},
	{expr: `*#$i{"k":$}[0]`, data: pairsJSON, want: `{"k":{"b":1,"c":2}}`},
	{expr: `a.b{"k":$i}#$i`, data: pairsJSON, want: `{"k":[0,0]}`},
	{expr: `a.b[0]{"k":$i}#$i`, data: pairsJSON, want: `{"k":0}`},
	{expr: `a.b{"k":$v}@$v`, data: pairsJSON, want: `{"k":[1,3]}`},
	{expr: `a.b[0]{"k":$v}@$v`, data: pairsJSON, code: "S0215"},
	{expr: `a[0]{"k":$.c}`, data: pairsJSON, want: `{"k":2}`},
	{expr: `"a"{"k":$}.b`, data: pairsJSON, want: undefined},
	{expr: `k{"x":$}#$i.$i`, data: `{"k":[1,2]}`, want: `{"x":[0,1]}`},
	{expr: `e#$i{"x":$i}.b`, data: `{"e":[{"b":1},{"b":2}]}`, want: `{"x":[0,1]}`},
	{expr: `e^(b){"x":b}#$i.b`, data: `{"e":[{"b":1},{"b":2}]}`, want: `{}`},
	{expr: `e@$v{"x":$v.b}.b`, data: `{"e":[{"b":1},{"b":2}]}`, want: `{}`},
	{expr: `"a"#$i.{"i":$i}`, data: pairsJSON, want: `[{"i":0},{"i":1}]`},
	{expr: `(a){"k":$.c}[]`, data: pairsJSON, want: `{"k":[2,4]}`},
	{expr: `a{b: c[]}`, data: `{"a":[{"b":"k","c":1}]}`, want: `{"k":[1]}`},
	{expr: `a{b: undefined}`, data: `{"a":[{"b":"k","c":1}]}`, want: `{}`},
	{expr: `a.[b,c]{"k":$}`, data: `{"a":[{"b":1,"c":2},{"b":3,"c":4}]}`, want: `{"k":[1,2,3,4]}`},
	{expr: `a.[b]{"k":$}`, data: `{"a":[{"b":1,"c":2},{"b":3,"c":4}]}`, want: `{"k":[1,3]}`},
	{expr: `a#$i.[b,c]{"k":$count($)}`, data: `{"a":[{"b":1,"c":2},{"b":3,"c":4}]}`, want: `{"k":4}`},
	{expr: `[[1,2],[3,4]]{"k":$}`, want: `{"k":[1,2,3,4]}`},
	{expr: `[[],[]]{"a":$}`, want: `{"a":[]}`},
	{expr: `[]{"a":1}`, want: `{"a":1}`},
	{expr: `e{"a":1}`, data: `{"e":[]}`, want: `{"a":1}`},
	{expr: `nope{"a":1}`, data: pairsJSON, want: `{"a":1}`},
	{expr: `a[b=9]{"a":$count($)}`, data: pairsJSON, want: `{"a":0}`},
	{expr: `a{"k": nope, "k": 1}`, data: pairsJSON, code: "D1009"},
	{expr: `$keys(a{"b" & b: 1, "c" & c: 2})`, data: pairsJSON, want: `["b1","c2","b3","c4"]`},
	{expr: `Account.Nope.Order#$o{"a":1}`, data: accountJSON, want: `{"a":1}`},
	{expr: `Account.Nope#$o.Order{"a":1}`, data: accountJSON, want: `{}`},
	{expr: `Account.Order.Product^(>Price)#$i.Name{"k":$i}`, data: accountJSON, want: `{"k":[0,1,2]}`},
	{expr: `a#$i.c[$>9]^($){"k":1}`, data: keyedJSON, want: `{}`},
	{expr: `a[b="j"].c^(%.b){"k":$}`, data: keyedJSON, want: `{"k":4}`},
	{expr: `a[b="j"]#$i.c^($)[$keys($)[0]="@"]`, data: keyedJSON, want: undefined},
	{expr: `nope^(%.b){"k":1}`, data: keyedJSON, want: `{}`},
	{expr: `e^(b)#$i{"k":1}`, data: keyedJSON, want: `{"k":1}`},
	{expr: `e^(b)#$i[0]{"k":1}`, data: keyedJSON, want: `{"k":1}`},
	{expr: `nope.x#$j^(b){"k":1}`, data: keyedJSON, want: `{"k":1}`},
	{expr: `a.nope#$j^(b){"k":1}`, data: keyedJSON, want: `{}`},
	{expr: `Account.Order.Product@$p{"k":$p.Name}`, data: accountJSON, want: `{"k":["Hat","Cap","Bag"]}`},
	{expr: `a{b: c, b: d}`, data: `{"a":{"b":"k","c":1,"d":2}}`, code: "D1009"},
	{expr: `a{b: $error("g")}`, data: `{"a":{"b":"k","c":1}}`, code: "D3137"},
	{expr: `a{$error("k"): 1}`, data: `{"a":{"b":"k","c":1}}`, code: "D3137"},
	{expr: `a{5: 1}`, data: `{"a":{"b":"k","c":1}}`, code: "T1003"},
	{expr: `{"a":1, "a":2}`, code: "D1009"},
	{expr: `{ $string(1): 2 }`, want: `{"1":2}`},
	{expr: `{1: 2}`, code: "T1003"},
	{expr: `[3,1,2]^($)`, want: `[1,2,3]`},
	{expr: `a^(v)`, data: `{"a":[{"v":1}]}`, want: `{"v":1}`},
	{expr: `[{"v":1}]^(v)`, want: `{"v":1}`},
	{expr: `a^(v)`, data: `{"a":[]}`, want: undefined},
	{expr: `a[]^(v)`, data: `{"a":[{"v":1}]}`, want: `[{"v":1}]`},
	{expr: `$^(n.%)`, data: `[{"n":"p","v":1}]`, want: `{"n":"p","v":1}`},
	{expr: `nn^($)[0]`, data: `{"nn":[[1,2]]}`, want: `[1,2]`},
	{expr: `nn^($)[1]`, data: `{"nn":[[1,2]]}`, want: undefined},
	{expr: `[{"a":"x"},{"a":1}]^(a)`, code: "T2007"},
	{expr: `[{"a":2},{"a":1}]^($error("s"))`, code: "D3137"},
	{expr: `a.[b,c]^($)[]`, data: pairsJSON, code: "T2008"},
	{expr: `a.[b,c]^(>$[0])`, data: pairsJSON, want: `[[3,4],[1,2]]`},
	{expr: `a.[b,c]^(>$[0])[]`, data: pairsJSON, want: `[[3,4],[1,2]]`},
	{expr: `a.[b,c]^(>$[0]).*`, data: pairsJSON, want: `[3,4,1,2]`},
	{expr: `a.[b,c]^(>$[0])[0]`, data: pairsJSON, want: `[3,4]`},
	{expr: `a.[b,c]^(>$[0])[0].$string()`, data: pairsJSON, want: `["3","4"]`},
	{expr: `a.[b,c]^(>$[0])[-1].$string()`, data: pairsJSON, want: `["1","2"]`},
	{expr: `a.[b,c]^(>$[0])[0][1]`, data: pairsJSON, want: `4`},
	{expr: `a.[b,c]^(>$[0])[[0]].$string()`, data: pairsJSON, want: `"[3,4]"`},
	{expr: `a.[b,c]^(>$[0])[$[0]>1][0].$string()`, data: pairsJSON, want: `["3","4"]`},
	{expr: `o.[b,c]^(>$[0])[0]`, data: pairsJSON, want: `[5,6]`},
	{expr: `o.[b,c]^(>$[0])[1]`, data: pairsJSON, want: undefined},
	{expr: `o.[b,c]^(>$[0])[$[0]=5]`, data: pairsJSON, want: `[5,6]`},
}

var transformCases = []exprCase{
	{
		expr: `$ ~> |Account.Order.Product|{"Total": Price*Qty}, ["Qty","Price"]|`, data: accountJSON,
		want: `{"Account":{"Name":"Firefly","Order":[{"OrderID":"o1","Product":[{"Name":"Hat","Total":20},` +
			`{"Name":"Cap","Total":5}]},{"OrderID":"o2","Product":[{"Name":"Bag","Total":20}]}]}}`,
	},
	{expr: `$ ~> |Account|{}, "Name"|`, data: `{"Account":{"Name":"Firefly"}}`, want: `{"Account":{}}`},
	{
		expr: `$ ~> |Account|{"x":1}|`, data: `[{"Account":{"Name":"a"}},{"Account":{"Name":"b"}}]`,
		want: `[{"Account":{"Name":"a","x":1}},{"Account":{"Name":"b","x":1}}]`,
	},
	{expr: `|Account|{"x":1}|(5)`, code: "T0410"},
	{expr: `b ~> |a|{"x":1}|`, data: `{"b":true}`, code: "T0410"},
	{expr: `missing ~> |a|{"x":1}|`, data: `{"b":1}`, want: undefined},
	{expr: `|Account|{"x":1}|()`, want: undefined},
	{expr: `$ ~> |a|{"x":1}, "y"|`, data: `{"a":5}`, want: `{"a":5}`},
	{expr: `$ ~> |a|5|`, data: `{"a":5}`, code: "T2011"},
	{expr: `$ ~> |a|{}, 5|`, data: `{"a":5}`, code: "T2012"},
	{expr: `$ ~> |a|$error("u")|`, data: `{"a":5}`, code: "D3137"},
	{expr: `$ ~> |a|{}, $error("d")|`, data: `{"a":5}`, code: "D3137"},
	{expr: `$ ~> |Account|5|`, data: `{"Account":{"Name":"Firefly"}}`, code: "T2011"},
	{expr: `$ ~> |Account|{}, 5|`, data: `{"Account":{"Name":"Firefly"}}`, code: "T2012"},
	{expr: `$ ~> |Account|$error("u")|`, data: `{"Account":{"Name":"Firefly"}}`, code: "D3137"},
	{expr: `$ ~> |Account|{}, $error("d")|`, data: `{"Account":{"Name":"Firefly"}}`, code: "D3137"},
	{expr: `({"x": o.b[]} ~> |x|{"z":1}|; o.b)`, data: `{"o":{"b":{"c":1}}}`, want: `{"c":1}`},
	{expr: `($o := {"z": $map([1], $keys)}; ($o ~> |$|{}|).z)`, want: `[]`},
	{expr: `$ ~> |o|{}, $map([1], $keys)|`, data: pairsJSON, want: `{"a":[{"b":1,"c":2},{"b":3,"c":4}],"o":{"b":5,"c":6}}`},
	{expr: `$ ~> |o|{}, $map([{"b":1}], $keys)|`, data: pairsJSON, want: `{"a":[{"b":1,"c":2},{"b":3,"c":4}],"o":{"c":6}}`},
	{expr: `$ ~> |o|{}, ["b"].[$]|`, data: pairsJSON, want: `{"a":[{"b":1,"c":2},{"b":3,"c":4}],"o":{"c":6}}`},
	{
		expr: `$ ~> |**[a]|{"p": a}|`, data: `{"x":{"a":{"a":{"v":1}}}}`,
		want: `{"x":{"a":{"a":{"v":1},"p":{"v":1}},"p":{"a":{"v":1},"p":{"v":1}}}}`,
	},
	{expr: `($ ~> |x.a|{"self": $}|).x.a.self.a.v`, data: `{"x":{"a":{"a":{"v":1}}}}`, want: `1`},
	{expr: `$ ~> |x|{}, [1]|`, data: `{"x":{"a":1}}`, code: "T2012"},
	{expr: `$ ~> |x|{}, ["a", null]|`, data: `{"x":{"a":1}}`, code: "T2012"},
	{expr: `({"a":[1, function(){1}]} ~> |$|{}|).a`, want: `[1,""]`},
	{expr: `({"o":{"f": function(){1}}} ~> |o|{"z":1}|)`, want: `{"o":{"f":"","z":1}}`},
}

var pathStepCases = []exprCase{
	{expr: `$sort($, function($a,$b){$a.v<$b.v}).n`, data: rootPairsJSON, want: `["q","p"]`},
	{expr: `$reverse($)[0].n`, data: rootPairsJSON, want: `"q"`},
	{expr: `$sum(v).$`, data: rootPairsJSON, want: `3`},
	{expr: `{n:1}.$`, data: rootPairsJSON, want: `{"p":1,"q":1}`},
	{expr: `({n:1}).$`, data: rootPairsJSON, want: `{"p":1,"q":1}`},
	{expr: `({n:1})`, data: rootPairsJSON, code: "T1003"},
	{expr: `{"a":$reverse($).n}`, data: rootPairsJSON, want: `{"a":["p","q"]}`},
	{expr: `{"a":{n:v}}`, data: rootPairsJSON, want: `{"a":{"p":1,"q":2}}`},
	{expr: `$eval("$reverse($).n").$`, data: rootPairsJSON, want: `["p","q"]`},
	{expr: `[{n:v}].$`, data: rootPairsJSON, code: "T1003"},
	{expr: `[$reverse($).n].$`, data: rootPairsJSON, want: `["q","p"]`},
	{expr: `({n:v}.$).$`, data: rootPairsJSON, want: `[{"p":1},{"q":2}]`},
	{expr: `{n:1}^(n)`, data: rootPairsJSON, want: `{"p":1,"q":1}`},
	{expr: `[{n:1}]^($).$`, data: rootPairsJSON, code: "T1003"},
	{expr: `{n:v}^(n)#$i.$i`, data: rootPairsJSON, want: `0`},
	{expr: `{n:v}^(n)^(v)#$i.$i`, data: rootPairsJSON, want: `0`},
	{expr: `n@$x.{"k":$}`, data: `[{"n":"p","v":1}]`, want: `{"k":{"n":"p","v":1}}`},
	{expr: `$@$x.{"k":$}`, data: `[{"n":"p","v":1}]`, want: `{"k":[{"n":"p","v":1}]}`},
	{expr: `1#$i`, want: `1`},
	{expr: `1#$i{"k":$i}`, want: `{"k":0}`},
	{expr: `a.true`, data: pairsJSON, code: "S0213"},
	{expr: `$x.1`, code: "S0213"},
	{expr: `null#$i.x`, code: "S0213"},
	{expr: `a.1[0]`, data: pairsJSON, code: "S0213"},
	{expr: `a.true[0][1]`, data: pairsJSON, code: "S0213"},
	{expr: `1[0].x`, code: "S0213"},
	{expr: `a.(1)[0]`, data: pairsJSON, want: `[1,1]`},
	{
		expr: `Account.Order.Product.{"n":Name,"acc":$$.Account.Name}`, data: accountJSON,
		want: `[{"acc":"Firefly","n":"Hat"},{"acc":"Firefly","n":"Cap"},{"acc":"Firefly","n":"Bag"}]`,
	},
	{expr: `Account.Order[0].Product[-1].Name`, data: accountJSON, want: `"Cap"`},
	{expr: `**.Name`, data: `{"a":{"Name":"x","b":[{"Name":"y"}]}}`, want: `["x","y"]`},
	{expr: `a.*.c`, data: `{"a":{"b":{"c":1},"d":{"c":2}}}`, want: `[1,2]`},
	{expr: `a.*[0]`, data: `{"a":{"b":[1,2],"d":[3]}}`, want: `1`},
	{expr: `$.a`, data: `{"a":1}`, want: `1`},
	{expr: `$sum(a.b[])`, data: `{"a":{"b":1}}`, want: `1`},
	{expr: `a[].b`, data: `{"a":{"b":1}}`, want: `[1]`},
	{expr: `a.b[]`, data: `{"a":{"b":1}}`, want: `[1]`},
	{expr: `a.(b; c)`, data: `{"a":{"b":1,"c":2}}`, want: `2`},
	{expr: `a.[b, c]`, data: `{"a":[{"b":1,"c":2},{"b":3,"c":4}]}`, want: `[[1,2],[3,4]]`},
	{expr: `a.[b]`, data: `{"a":[{"b":1},{"b":3}]}`, want: `[[1],[3]]`},
	{expr: `[a.b]`, data: `{"a":[{"b":1},{"b":3}]}`, want: `[1,3]`},
	{expr: `[[1,2]][0]`, want: `[1,2]`},
	{expr: `a[b > 1][0].b`, data: `{"a":[{"b":1},{"b":3},{"b":5}]}`, want: `3`},
	{expr: `a[[0,1]]`, data: `{"a":[1,2,3]}`, want: `[1,2]`},
	{expr: `a[-5]`, data: `{"a":[1,2,3]}`, want: undefined},
	{expr: `a["x"]`, data: `{"a":[1,2,3]}`, want: `[1,2,3]`},
	{expr: `($f := function($x){ $x > 1 }; a[$f($)])`, data: `{"a":[1,2,3]}`, want: `[2,3]`},
	{expr: `a.$string()`, data: `{"a":[1,2]}`, want: `["1","2"]`},
	{expr: `a.$`, data: `{"a":[1,2]}`, want: `[1,2]`},
	{expr: `a.$$`, data: `{"a":[1,2]}`, want: `[{"a":[1,2]},{"a":[1,2]}]`},
	{expr: `a.$$`, data: `{"a":1}`, want: `{"a":1}`},
	{expr: `($v := "z"; a.$v)`, data: `{"a":[1,2]}`, want: `["z","z"]`},
	{expr: `($v := "z"; a.$v)`, data: `{"a":[]}`, want: undefined},
	{expr: `($v := "z"; [1,2].$v)`, want: `["z","z"]`},
	{expr: `($v := [7,8]; a.$v)`, data: `{"a":[1,2]}`, want: `[7,8,7,8]`},
	{expr: `($v := {"k":1}; a.$v.k)`, data: `{"a":[1,2]}`, want: `[1,1]`},
	{expr: `($v := "z"; $count(a.$v))`, data: `{"a":[1,2,3]}`, want: `3`},
	{expr: `($v := "z"; a.[b, c].$v)`, data: `{"a":{"b":1,"c":2}}`, want: `"z"`},
	{expr: `($v := "z"; a.[b, c].$v)`, data: `{"a":[{"b":1,"c":2},{"b":3,"c":4}]}`, want: `["z","z"]`},
	{expr: `($v := "z"; a.[b, c][].$v)`, data: `{"a":{"b":1,"c":2}}`, want: `["z"]`},
	{expr: `($v := ["z"]; a.$v[])`, data: `{"a":[1,2]}`, want: `["z","z"]`},
	{expr: `($v := "z"; a.$v[])`, data: `{"a":1}`, want: `["z"]`},
	{expr: `($v := ["z","y"]; a.$v)`, data: `{"a":1}`, want: `["z","y"]`},
	{expr: `a.true`, data: `{"a":[1,2]}`, code: "S0213"},
	{expr: `a.null`, data: `{"a":[1,2]}`, code: "S0213"},
	{expr: `a.false.b`, data: `{"a":[1,2]}`, code: "S0213"},
	{expr: `true.a`, code: "S0213"},
	{expr: `a.true[0]`, data: `{"a":[1,2]}`, code: "S0213"},
	{expr: `a.1[0]`, data: `{"a":[1,2]}`, code: "S0213"},
	{expr: `false and a.true`, code: "S0213"},
	{expr: `a.(true)`, data: `{"a":[1,2]}`, want: `[true,true]`},
	{expr: `a.[$].$`, data: `{"a":[1,2]}`, want: `[[1],[2]]`},
	{expr: `a.[$].$.$`, data: `{"a":[1,2]}`, want: `[[1],[2]]`},
	{expr: `a.[$, 1].$.$`, data: `{"a":[1,2]}`, want: `[[1,1],[2,1]]`},
	{expr: `a.[b].$`, data: `{"a":[{"b":1}]}`, want: `[1]`},
	{expr: `[1].[$].$`, want: `[1]`},
	{expr: `a.[b, c]`, data: `{"a":[{"x":1}]}`, want: `[]`},
	{expr: `a.[b, c].$`, data: `{"a":[{"x":1}]}`, want: `[]`},
	{expr: `($v := "z"; a.[b, c].$v)`, data: `{"a":[{"x":1}]}`, want: `"z"`},
	{expr: `a.[b, c].$$.a`, data: `{"a":[{"x":1}]}`, want: `[{"x":1}]`},
	{expr: `$exists(a.[b])`, data: `{"a":[{"x":1}]}`, want: `true`},
	{expr: `a.[b, c].$string()`, data: `{"a":[{"b":1,"c":2}]}`, want: `"[1,2]"`},
	{expr: `a.[b, c].$string()`, data: `{"a":[{"b":1,"c":2},{"b":3}]}`, want: `["[1,2]","[3]"]`},
	{expr: `a.[b, c].($count($))`, data: `{"a":[{"b":1,"c":2}]}`, want: `2`},
	{expr: `a.[b, c].$[0]`, data: `{"a":[{"b":1,"c":2}]}`, want: `1`},
	{expr: `a.[b, c].x[0]`, data: `{"a":[{"b":{"x":[1,5]},"c":{"x":2}}]}`, want: `1`},
	// A field with a predicate is a one-step path, which maps over an array
	// of contexts.
	{expr: `q.(a[0])`, data: `{"q":[[{"a":[1,2]},{"a":[3]}]]}`, want: `[1,3]`},
	// A variable or a constructor is evaluated once, against the whole array.
	{expr: `q.($[0].a)`, data: `{"q":[[{"a":5},{"a":6}]]}`, want: `5`},
	{expr: `q.([$][0].a)`, data: `{"q":[[[{"a":1}],[{"a":2}]]]}`, want: `1`},
	{expr: `a.[b, c].*`, data: `{"a":[{"b":[{"x":1},{"y":3}],"c":{"x":2}}]}`, want: `[{"x":1},{"y":3},{"x":2}]`},
	{expr: `a.[b].*`, data: `{"a":[{"x":1}]}`, want: undefined},
	{expr: `a.[b].*`, data: `{"a":[{"b":{"x":1}}]}`, want: `{"x":1}`},
	{expr: `a.[b, c].{$string($): $count($)}`, data: `{"a":[{"b":1,"c":1}]}`, want: `{"1":2}`},
	{expr: `a.[b, c].{"k": $, "j": $[0]}`, data: `{"a":[{"b":1,"c":2}]}`, want: `{"j":1,"k":[1,2]}`},
	{expr: `a.[b, c].{"k": $}`, data: `{"a":[{"b":1,"c":2},{"b":3,"c":4}]}`, want: `[{"k":[1,2]},{"k":[3,4]}]`},
	{expr: `a.[b, c].[$].$string()`, data: `{"a":{"b":1,"c":2}}`, want: `"[1,2]"`},
	{expr: `a.[b, c].[$].$string()`, data: `{"a":[{"b":1,"c":2}]}`, want: `"[1,2]"`},
	{expr: `a.[b, c]{$string($): $}`, data: `{"a":{"b":1,"c":2}}`, want: `{"1":1,"2":2}`},
	{expr: `(a.[b, c]){$string($): $}`, data: `{"a":{"b":1,"c":2}}`, want: `{"1":1,"2":2}`},
	{expr: `a.[b, c]{$string($): $}`, data: `{"a":[{"b":1,"c":2},{"b":1,"c":3}]}`, want: `{"[1,2]":[1,2],"[1,3]":[1,3]}`},
	{expr: `a.[b, c][]`, data: `{"a":{"b":1,"c":2}}`, want: `[[1,2]]`},
	{expr: `a.[b][]`, data: `{"a":{"x":1}}`, want: `[[]]`},
	{expr: `$.[1,2][]`, data: `{"a":1}`, want: `[[1,2]]`},
	{expr: `a.[b, c].[$][]`, data: `{"a":[{"b":1,"c":2}]}`, want: `[[1,2]]`},
	{expr: `a.[b, c][]{$string($): $}`, data: `{"a":{"b":1,"c":2}}`, want: `{"[1,2]":[1,2]}`},
	{expr: `a.[b,c].($)`, data: pairsJSON, want: `[[1,2],[3,4]]`},
	{expr: `a.[b,c].($).$string()`, data: pairsJSON, want: `["[1,2]","[3,4]"]`},
	{expr: `a.[b,c].($).$count($)`, data: pairsJSON, want: `[2,2]`},
	{expr: `a.[b,c].(true ? $ : 0).$string()`, data: pairsJSON, want: `["[1,2]","[3,4]"]`},
	{expr: `a.[b,c].($x := $; $x).$string()`, data: pairsJSON, want: `["[1,2]","[3,4]"]`},
	{expr: `a.[b,c].$sum($)`, data: pairsJSON, want: `[3,7]`},
	{expr: `a.[b,c].[$, 9].$string()`, data: pairsJSON, want: `["[1,2,9]","[3,4,9]"]`},
	{expr: `a.[[b,c]].*`, data: pairsJSON, want: `[1,2,3,4]`},
	{expr: `x.*`, data: nestedJSON, want: undefined},
	{expr: `y.*`, data: nestedJSON, want: `[1,2,3]`},
	{expr: `z.*`, data: nestedJSON, want: `[1,3,4]`},
	{expr: `m.*`, data: nestedJSON, want: `[1,2,{"c":3}]`},
	{expr: `[1,2].*`, want: undefined},
	{expr: `[[1,2],[3]].*`, want: `[1,2,3]`},
	{expr: `*`, data: `[1,[2,[3]]]`, want: `[1,2,3]`},
	{expr: `$.*`, data: `[{"a":1},{"b":[2,[3]]}]`, want: `[1,2,3]`},
	{expr: `*`, data: `[{"a":1},{"b":[2,[3]]}]`, want: `[{"a":1},{"b":[2,[3]]}]`},
	{expr: `**`, data: `{"a":[1,[2]]}`, want: `[{"a":[1,[2]]},1,2]`},
	{expr: `z.**`, data: nestedJSON, want: `[{"a":1},1,2,3,4]`},
	{expr: `a.[b,c].**`, data: pairsJSON, want: `[1,2,3,4]`},
	{expr: `o.[b,c].**`, data: pairsJSON, want: `[5,6]`},
	{expr: `a.[b,c]^(>$[0])[0]^(>$)[0]`, data: pairsJSON, want: `4`},
	{expr: `y.(*)`, data: nestedJSON, want: `[1,2,3]`},
	{expr: `y.[*]`, data: nestedJSON, want: `[[1,2],[3]]`},
	{expr: `y#$i.*`, data: nestedJSON, want: `[1,2,3]`},
	{expr: `m.*`, data: `{"m":{"a":[1]}}`, want: `[1]`},
	{expr: `*`, data: `[[]]`, want: `[]`},
	{expr: `y.*`, data: `{"y":[[[1]],[]]}`, want: `[1]`},
	{expr: `y.*`, data: `{"y":[[[]]]}`, want: `[]`},
	{expr: `$count(y.**)`, data: nestedJSON, want: `3`},
	{expr: `$eval("*", y)`, data: nestedJSON, want: `[1,2,3]`},
	{expr: `$eval("*", z)`, data: nestedJSON, want: `[{"a":1},2,3,4]`},
	{expr: `$map([1], function($v){*})`, data: `[{"a":1},{"b":2}]`, want: `[{"a":1},{"b":2}]`},
	{expr: `a.$`, data: `{"a":[[1,2],[3]]}`, want: `[1,2,3]`},
	{expr: `($v := [[1,2]]; [1].$v)`, want: `[[1,2]]`},
	{expr: `a[$error("f")]`, data: `{"a":[{"b":1}]}`, code: "D3137"},
}

var subscriptCases = []exprCase{
	{expr: `[1,2,3][-0.5]`, want: `3`},
	{expr: `[1,2,3][[0,0]]`, want: `[1,1]`},
	{expr: `[1,2,3][[2,0]]`, want: `[1,3]`},
	{expr: `[1,2,3][0/0]`, want: undefined},
	{expr: `[1,2,3][1/0]`, code: "D1001"},
	{expr: `[1,2,3][[-1/0]]`, code: "D1001"},
	{expr: `a#$i[[0,0]]`, data: `{"a":[1,2,3]}`, want: `[1,1]`},
	{expr: `a#$i[-0.5]`, data: `{"a":[1,2,3]}`, want: `3`},
	{expr: `a#$i[1/0]`, data: `{"a":[1,2,3]}`, code: "D1001"},
	{expr: `a#$i[[1/0]]`, data: `{"a":[1,2,3]}`, code: "D1001"},
	{expr: `a[["x",1/0]]`, data: `{"a":[1,2,3]}`, code: "D1001"},
	{expr: `a#$i[[0,"x",1/0]]`, data: `{"a":[1,2,3]}`, code: "D1001"},
	{expr: `a#$i[[0,"x"]]`, data: `{"a":[1,2,3]}`, want: `[1,2,3]`},
	{expr: `$boolean(0/0)`, want: `false`},
	{expr: `[[1,2],[3]][[0]]`, want: `[1,2]`},
	{expr: `n[[0]]`, data: `{"n":[[2,1],[3,4]]}`, want: `[2,1]`},
	{expr: `[[1,2],[3]][$[0]=1][]`, want: `[[1,2]]`},
	{expr: `[[1,2],[3]][0][]`, want: `[1,2]`},
	{expr: `a[b]`, data: `{"a":[{"b":1},{"b":0}]}`, want: undefined},
	{expr: `a[b-1]`, data: `{"a":[{"b":1},{"b":0}]}`, want: `[{"b":1},{"b":0}]`},
	{expr: `[1,2,3][$ > 1 ? 0 : 1]`, want: undefined},
	{expr: `[1,2,3][[2,0,0]]`, want: `[1,1,3]`},
	{expr: `[[1,2],[3]][0+0][]`, want: `[[1,2]]`},
	{expr: `[1,2,3][[0]]`, want: `1`},
	{expr: `[[1,2],[3]][[1]]`, want: `[3]`},
	{expr: `[1,2,3][[0]][]`, want: `[1]`},
	{expr: `[1,2,3][[5]][]`, want: undefined},
	{expr: `a[[1]][0]`, data: `{"a":[[1,2],[3,4]]}`, want: `[3,4]`},
	{expr: `a[[1]][$=4]`, data: `{"a":[[1,2],[3,4]]}`, want: undefined},
	{expr: `a[[0]][[0]]`, data: `{"a":[[1,2],[3,4]]}`, want: `[1,2]`},
	{expr: `a[[0]][]`, data: `{"a":[[1,2],[3,4]]}`, want: `[[1,2]]`},
	{expr: `$count(a[[0]])`, data: `{"a":[[1,2],[3,4]]}`, want: `2`},
	{expr: `[a[[0]]]`, data: `{"a":[[1,2],[3,4]]}`, want: `[1,2]`},
	// A filter's result is a sequence, whose items are the next step's
	// contexts, so a lone array stays one context.
	{expr: `y[[0]].$count($)`, data: `{"y":[[1,2],[3]],"z":[{"a":[[1,2]]},{"a":[[3]]}],"n":[1,2,3]}`, want: `2`},
	{expr: `z.a[[0]]`, data: `{"y":[[1,2],[3]],"z":[{"a":[[1,2]]},{"a":[[3]]}],"n":[1,2,3]}`, want: `[[1,2],[3]]`},
	{expr: `y[$count($)=2].$count($)`, data: `{"y":[[1,2],[3]],"z":[{"a":[[1,2]]},{"a":[[3]]}],"n":[1,2,3]}`, want: `2`},
	{expr: `z.a[$count($)>0]`, data: `{"y":[[1,2],[3]],"z":[{"a":[[1,2]]},{"a":[[3]]}],"n":[1,2,3]}`, want: `[[1,2],[3]]`},
	{expr: `y[[0]]^($)[0]`, data: `{"y":[[1,2],[3]],"z":[{"a":[[1,2]]},{"a":[[3]]}],"n":[1,2,3]}`, want: `[1,2]`},
	// A sort's result is a sequence too.
	{expr: `[3]^($)`, want: `3`},
	{expr: `[3]^($)[]`, want: `[3]`},
	{expr: `$count(n^($))`, data: `{"y":[[1,2],[3]],"z":[{"a":[[1,2]]},{"a":[[3]]}],"n":[1,2,3]}`, want: `3`},
	{expr: `q[[0]]{"k":$}`, data: `{"q":[[1,2,3]]}`, want: `{"k":[1,2,3]}`},
	{expr: `q^(t){t:1}`, data: `{"q":[[{"t":"a"},{"t":"b"}]]}`, code: "T1003"},
	{expr: `a^($) & "x"`, data: `{"a":[3,1,2]}`, want: `"[1,2,3]x"`},
	{expr: `($x := s^($); $x + 1)`, data: `{"s":[3]}`, want: `4`},
	// ** includes its input and walks into arrays without yielding them;
	// * flattens array values completely.
	{expr: `$count(**)`, data: `{"a":{"b":[1,[2,3]]}}`, want: `5`},
	{expr: `a.*`, data: `{"a":{"b":[[1,[2]],{"c":3}]}}`, want: `[1,2,{"c":3}]`},
	{expr: `o.*`, data: `{"o":[{"v":[1,2]},{"v":3}]}`, want: `[1,2,3]`},
	{expr: `y.*`, data: `{"y":[[1,2],[3]]}`, want: `[1,2,3]`},
	{expr: `n.*`, data: `{"n":[1,2,3]}`, want: undefined},
	{expr: `{"a":[5]}.*`, want: `[5]`},
	{expr: `{"a":[[]]}.*`, want: `[]`},
	{expr: `*`, data: `[{"a":1},2,[3,[4]]]`, want: `[{"a":1},2,3,4]`},
	{expr: `$.*`, data: `[{"a":1},2,[3,[4]]]`, want: `[1,3,4]`},
	{expr: `*.a`, data: `[{"a":1},{"a":2}]`, want: `[1,2]`},
	{expr: `*.*`, data: `[{"a":1},{"b":[2,3]}]`, want: `[1,2,3]`},
	{expr: `*#$i.$i`, data: `[{"a":1},{"b":[2,3]}]`, want: `[0,1]`},
	{expr: `$string(**)`, data: `{"s":[3]}`, want: `"[{\"s\":[3]},3]"`},
}

var operatorCases = []exprCase{
	{expr: `[3..1]`, want: `[]`},
	{expr: `[1.5..3]`, code: "T2003"},
	{expr: `["a"..3]`, code: "T2003"},
	{expr: `[1..1e10]`, code: "D2014"},
	{expr: `[1..$error("r")]`, code: "D3137"},
	{expr: `[$error("l")..3]`, code: "D3137"},
	{expr: `false ? 1`, want: undefined},
	{expr: `$error("x") ? 1 : 2`, code: "D3137"},
	{expr: `($x := [1,2]; $x[0])`, want: `1`},
	{expr: `$substring(?, 1)("abc")`, want: `"bc"`},
	{expr: `$substring(?, ?)("abc", 1)`, want: `"bc"`},
	{expr: `?`, code: "S0211"},
	{expr: `[1, ?]`, code: "S0211"},
	{expr: `1 + ?`, code: "S0211"},
	{expr: `$f := ?`, code: "S0211"},
	{expr: `a[?]`, code: "S0211"},
	{expr: `{"a": ?}`, code: "S0211"},
	{expr: `$substring((?), 1)`, code: "S0211"},
	{expr: `$f(? + 1)`, code: "S0202"},
	{expr: `$f(?[0])`, code: "S0202"},
	{expr: `function(?){1}`, code: "S0208"},
	{expr: `$undefinedFn(?, 1)`, code: "T1008"},
	{expr: `($f := function(){1}; f(?))`, code: "T1007"},
	{expr: `$foo()`, code: "T1006"},
	{expr: `5()`, code: "T1006"},
	{expr: `"a" ~> $uppercase`, want: `"A"`},
	{expr: `[1,2] ~> $sum`, want: `3`},
	{expr: `($f := function($x){$x+1}; 5 ~> $f)`, want: `6`},
	{expr: `"x" ~> 5`, code: "T2006"},
	{expr: `(1; 2; )`, want: `2`},
	{expr: `[1,2,3][$ > 1]`, want: `[2,3]`},
	{expr: `"a" < 1`, code: "T2009"},
	{expr: `null + 1`, code: "T2001"},
	{expr: `1 + null`, code: "T2002"},
	{expr: `nothing + "a"`, code: "T2002"},
	{expr: `"a" + nothing`, code: "T2001"},
	{expr: `nothing + 1`, want: undefined},
	{expr: `5 in o.[b,c]`, data: pairsJSON, want: `true`},
	{expr: `[o.[b,c], 1]`, data: pairsJSON, want: `[5,6,1]`},
	{expr: `[[o.[b,c]]]`, data: pairsJSON, want: `[[5,6]]`},
}

// keepArrayCases cover the [] operator: it keeps a one-item result as an
// array, which a later path step still flattens like any sequence.
var keepArrayCases = []exprCase{
	{expr: `o.b[]`, data: pairsJSON, want: `[5]`},
	{expr: `o.(b[])`, data: pairsJSON, want: `5`},
	{expr: `o.($.b[])`, data: pairsJSON, want: `5`},
	{expr: `a.(b[])`, data: pairsJSON, want: `[1,3]`},
	{expr: `($x := o.b[]; $x)`, data: pairsJSON, want: `[5]`},
	{expr: `($x := o.b[]; o.$x)`, data: pairsJSON, want: `5`},
	{expr: `($x := o.b[]; a.$x)`, data: pairsJSON, want: `[5,5]`},
	{expr: `($f := function(){o.b[]}; $f())`, data: pairsJSON, want: `[5]`},
	{expr: `o^(b)[]`, data: pairsJSON, want: `[{"b":5,"c":6}]`},
	{expr: `o.(b^($)[])`, data: pairsJSON, want: `5`},
	{expr: `o.[b,c]^(>$[0])[]`, data: pairsJSON, want: `[[5,6]]`},
	{expr: `o.{"k": b[]}`, data: pairsJSON, want: `{"k":[5]}`},
	{expr: `{"k": o.b[]}.k`, data: pairsJSON, want: `5`},
	{expr: `a#$i.b[]`, data: pairsJSON, want: `[1,3]`},
	{expr: `o#$i.[b,c][]`, data: pairsJSON, want: `[5,6]`},
	{expr: `b[]`, data: `{"b":5}`, want: `[5]`},
	{expr: `$type(b[])`, data: `{"b":5}`, want: `"array"`},
	{expr: `a{b: c[]}`, data: `{"a":[{"b":"k","c":1},{"b":"j"}]}`, want: `{"k":[1]}`},
	{expr: `a{b: (c)[]}`, data: `{"a":[{"b":"k","c":1},{"b":"j"}]}`, want: `{"k":1}`},
	{expr: `x.(x ? [1])`, data: `{"x":[{"x":true},{}]}`, want: `[1]`},
	{expr: `[1,2][$>5][]`, want: undefined},
	{expr: `[{"k": o.b[]},{"k": 2}].k`, data: pairsJSON, want: `[5,2]`},
	{expr: `[{"k": o.b[]}][k = 5]`, data: pairsJSON, want: `{"k":[5]}`},
	{expr: `($o := {"k": o.b[]}; [$o, $o].k)`, data: pairsJSON, want: `[5,5]`},
	{expr: `[{"k": o.[b,c]}, {"k": 1}].k`, data: pairsJSON, want: `[[5,6],1]`},
	{expr: `$map([1], function($v){$$.o.b[]}).($type($))`, data: pairsJSON, want: `"number"`},
	{expr: `($x := o[]; $x^(b))`, data: pairsJSON, want: `{"b":5,"c":6}`},
	{expr: `o.b[]^($)`, data: pairsJSON, want: `[5]`},
	{expr: `(o.b[])^($)`, data: pairsJSON, want: `5`},
}

// regexEscapeCases pin regex literals whose escaped }, ], ) or / sits inside
// a group or outside one; jsonata-js reads each to its closing /.
var regexEscapeCases = []exprCase{
	{expr: `$contains("x", /(\})/)`, want: `false`},
	{expr: `$contains("a}", /(\})/)`, want: `true`},
	{expr: `$contains("a}", /a\}/)`, want: `true`},
	{expr: `$contains("a]", /(\])/)`, want: `true`},
	{expr: `$contains("a]", /a\]/)`, want: `true`},
	{expr: `$contains("a)", /(\))/)`, want: `true`},
	{expr: `$contains("a)", /a\)/)`, want: `true`},
	{expr: `$contains("a/b", /(\/)/)`, want: `true`},
	{expr: `$contains("a/b", /\//)`, want: `true`},
	{expr: `$match("{}}", /(\}){2}/).match`, want: `"}}"`},
	{expr: `$replace("a}b", /([\}\)])/, "-")`, want: `"a-b"`},
}

// regexPositionCases pin where jsonata-js lexes '/' as division rather than a
// regex. It is division right after an opening [ or (, an object
// constructor's {, a unary - or a transform's opening |, and after the ] of a
// predicate or the ) of a block or lambda parameter list. It is a regex right
// after an empty [], a sort's ), a lambda body or a transform's closing |.
var regexPositionCases = []exprCase{
	{expr: `[/a/]`, code: "S0211"},
	{expr: `(/a/)`, code: "S0211"},
	{expr: `{/a/: 1}`, code: "S0211"},
	{expr: `-/a/`, code: "S0211"},
	{expr: `|/a/|{}|`, code: "S0211"},
	{expr: `[/* c */ /a/]`, code: "S0211"},
	{expr: `x.(/a/)`, code: "S0211"},
	{expr: `(/a/; 1)`, code: "S0211"},
	{expr: `$match("abc", /b/).index`, want: `1`},
	{expr: `("abc" ~> /b/).start`, want: `1`},
	{expr: `["abc", "x"][$contains($, /b/)]`, want: `"abc"`},
	{expr: `$count([1, /a/])`, want: `2`},
	{expr: `(1; $contains("abc", /b/))`, want: `true`},
	{expr: `{"k": $contains("abc", /b/)}`, want: `{"k":true}`},
	{expr: `"abc"{"k": $contains($, /b/)}`, want: `{"k":true}`},
	{expr: `(1)/2`, want: `0.5`},
	{expr: `-4/2`, want: `-2`},
	{expr: `[4, 6][0]/2`, want: `2`},
	{expr: `(1; 4)/2`, want: `2`},
	{expr: `("x"; "abc") ~> /b/`, want: `{"end":2,"groups":[],"match":"b","start":1}`},
	{expr: `["abc"][0] ~> /b/`, want: `{"end":2,"groups":[],"match":"b","start":1}`},
	{expr: `[4, 6][]/2`, code: "S0302"},
	{expr: `|a|{}|/2`, code: "S0302"},
	{expr: `[2,1]^($)/2`, code: "S0302"},
	{expr: `function($x){$x}/2`, code: "S0302"},
	{expr: `function($x)/2`, code: "S0202"},
	{expr: `function($x/){1}`, code: "S0211"},
}

// syntaxErrorCases pin the jsonata-js 2.2.2 code for malformed expressions.
// Missing tokens are S0203 at the end of the input and S0202 elsewhere; a
// missing operand is S0207, but only once nothing earlier fails.
var syntaxErrorCases = []exprCase{
	{expr: `1 +`, code: "S0207"},
	{expr: `$x :=`, code: "S0207"},
	{expr: `[1,`, code: "S0203"},
	{expr: `(1 +`, code: "S0203"},
	{expr: `{"a": 1`, code: "S0203"},
	{expr: `function($a)<n`, code: "S0203"},
	{expr: `function($a)<n{1}`, code: "S0202"},
	{expr: `[1 2]`, code: "S0202"},
	{expr: `[1 ~ 2]`, code: "S0204"},
	{expr: `!x`, code: "S0204"},
	{expr: `1 ~ 2`, code: "S0204"},
	{expr: `~|a|{}|`, code: "S0204"},
	{expr: `1|1`, code: "S0201"},
	{expr: `$str|ing()`, code: "S0201"},
	{expr: `"x" #in ["a"]`, code: "S0214"},
	{expr: `a^(x)@`, code: "S0214"},
	{expr: `a[0]@$x`, code: "S0215"},
	{expr: `a^(x)@$v`, code: "S0216"},
	{expr: `a{"k":1}[`, code: "S0203"},
	{expr: `$x{"k":1}[0]`, code: "S0209"},
	{expr: `$f(){"k":$}[0]`, code: "S0209"},
	{expr: `[1]{"k":$}[0]`, code: "S0209"},
	{expr: `(o){"k":$}[0][1]`, code: "S0209"},
	{expr: `(o){"k":$}[0]@$x`, code: "S0209"},
	{expr: `$f(){"k":$}{"k":$}[0]`, code: "S0210"},
	{expr: `(o){"k":$}{"k":$}[0]`, code: "S0210"},
	{expr: `(o){"k":$}[0] + )`, code: "S0211"},
	{expr: `$f(){"k":$}[0] , 1`, code: "S0201"},
	{expr: `[1]{"k":$}[0].{"a" 1}`, code: "S0202"},
	{expr: `a{"k":1}{"j":2}`, code: "S0210"},
	{expr: `2.5e`, code: "S0201"},
	{expr: `"\u+00e9"`, code: "S0104"},
	{expr: `"\u00`, code: "S0101"},
	{expr: `"abc\`, code: "S0103"},
	{expr: `/a/x`, code: "S0201"},
	{expr: `/)a/`, code: "S0302"},
	{expr: `/a)/`, code: "S0302"},
	{expr: `2.5e3`, want: `2500`},
	{expr: `$match("a(", /\(/).index`, want: `1`},
	{expr: `$ ~> |a ? b : c|{"d": 1}|`, data: `{"a":true,"b":{}}`, want: `{"a":true,"b":{"d":1}}`},
}

// lambdaParameterCases pin that jsonata-js parses each lambda parameter as an
// expression and only then requires a $variable (S0208), so a malformed
// parameter reports its parse error instead.
var lambdaParameterCases = []exprCase{
	{expr: `function($x/2){1}`, code: "S0208"},
	{expr: `function($x.y){1}`, code: "S0208"},
	{expr: `function($x, $y/2){1}`, code: "S0208"},
	{expr: `function(1){1}`, code: "S0208"},
	{expr: `function($x#$i){1}`, code: "S0208"},
	{expr: `function($x@$y){1}`, code: "S0208"},
	{expr: `function($x{"a":1}){1}`, code: "S0208"},
	{expr: `function($x 1){1}`, code: "S0202"},
	{expr: `function($x/2 1){1}`, code: "S0202"},
	{expr: `function("a" 1){1}`, code: "S0202"},
	{expr: `function($x + ){1}`, code: "S0211"},
	{expr: `function($x[]){$x}([1])`, want: `[1]`},
	{expr: `function($x, $y){$x+$y}(1,2)`, want: `3`},
	{expr: `function($x)<n:n>{$x}(2)`, want: `2`},
}

func TestPathAndOperatorSemantics(t *testing.T) {
	t.Run("parent operator", func(t *testing.T) { runExprCases(t, parentOperatorCases) })
	t.Run("binding operators", func(t *testing.T) { runExprCases(t, bindingOperatorCases) })
	t.Run("group and sort", func(t *testing.T) { runExprCases(t, groupAndSortCases) })
	t.Run("transform", func(t *testing.T) { runExprCases(t, transformCases) })
	t.Run("path steps", func(t *testing.T) { runExprCases(t, pathStepCases) })
	t.Run("subscripts", func(t *testing.T) { runExprCases(t, subscriptCases) })
	t.Run("keep array", func(t *testing.T) { runExprCases(t, keepArrayCases) })
	t.Run("operators", func(t *testing.T) { runExprCases(t, operatorCases) })
	t.Run("regex position", func(t *testing.T) { runExprCases(t, regexPositionCases) })
	t.Run("regex escapes", func(t *testing.T) { runExprCases(t, regexEscapeCases) })
	t.Run("lambda parameters", func(t *testing.T) { runExprCases(t, lambdaParameterCases) })
	t.Run("syntax errors", func(t *testing.T) { runExprCases(t, syntaxErrorCases) })
}

// TestParentAfterJoinOnGoMaps evaluates % after joins over data decoded by
// encoding/json: Go maps and slices are not comparable, so join parents are
// compared by identity.
func TestParentAfterJoinOnGoMaps(t *testing.T) {
	testCases := []exprCase{
		{expr: `library.loans@$l.books@$b[$l.isbn=$b.isbn].%.loans[0].customer`, data: libraryJSON, want: `"c1"`},
		{expr: `library.loans@$l.books@$b.%.books[1].title`, data: libraryJSON, want: `"B"`},
		{expr: `$count(library.loans@$l.books@$b.%.%)`, data: libraryJSON, want: `6`},
		{expr: `library.books@$b.loans@$l[$l.isbn=$b.isbn].%.title`, data: libraryJSON, want: undefined},
		{expr: `k@$a.m@$b.%.m`, data: rootArrayJSON, want: `[3,3,3]`},
		// The nested join's parents differ from the outer join's.
		{expr: `a.b@$x[$count(c@$y.%.%.%)>=0].$x.k`, data: nestedJoinJSON, want: `["x","y"]`},
	}
	for _, tC := range testCases {
		t.Run(tC.expr, func(t *testing.T) { tC.runDecoded(t, decodeGoMaps) })
	}
}

// encoding/json decodes a JSON null to nil, which an array's items keep as
// null rather than dropping as undefined.
func TestNullItemsOnGoMaps(t *testing.T) {
	testCases := []struct {
		expr string
		want string
	}{
		{expr: `a#$j`, want: `[4,null]`},
		{expr: `a#$j.$j`, want: `[0,1]`},
		{expr: `a@$v.$v`, want: `[4,null]`},
		{expr: `p.a#$j`, want: `[4,null]`},
		{expr: `$count(*[$=null])`, want: `1`},
	}
	var data any
	if err := json.Unmarshal([]byte(`{"a":[4,null],"p":{"a":[4,null]}}`), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, tC := range testCases {
		t.Run(tC.expr, func(t *testing.T) {
			e, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got, err := e.Eval(context.Background(), data)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if r := render(t, got); r != tC.want {
				t.Fatalf("got %s, want %s", r, tC.want)
			}
		})
	}
}

// The raw-JSON APIs leave to the evaluator what gjson would read differently:
// an all-digit path component, which gjson reads as an array index, and a
// path that binds or groups a tuple stream.
func TestRawJSONPathsLeftToEvaluator(t *testing.T) {
	runExprCasesAllAPIs(t, []exprCase{
		{expr: "x.`0`", data: `{"x":[5,6]}`, want: undefined},
		{expr: "`0`", data: `[5,6]`, want: undefined},
		{expr: "x.`0`", data: `{"x":{"0":7}}`, want: `7`},
		{expr: "x.`12`.y", data: `{"x":{"12":{"y":3}}}`, want: `3`},
		{expr: "$type(a#$j)", data: `{"a":[null]}`, want: `"null"`},
		{expr: "a#$j", data: `{"a":[[1,2]]}`, want: `[1,2]`},
		{expr: `P{"k":$}`, data: `{"P":[1,2]}`, want: `{"k":[1,2]}`},
	})
}
