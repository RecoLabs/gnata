package gnata_test

import "testing"

const (
	accountJSON = `{"Account":{"Name":"Firefly","Order":[` +
		`{"OrderID":"o1","Product":[{"Name":"Hat","Price":10,"Qty":2},{"Name":"Cap","Price":5,"Qty":1}]},` +
		`{"OrderID":"o2","Product":[{"Name":"Bag","Price":20,"Qty":1}]}]}}`
	libraryJSON = `{"library":{"books":[{"title":"A","isbn":"1"},{"title":"B","isbn":"2"}],` +
		`"loans":[{"isbn":"1","customer":"c1"},{"isbn":"2","customer":"c2"},{"isbn":"1","customer":"c3"}]}}`
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
	{expr: `Account.Order.Product{%.OrderID: $sum(Price)}`, data: accountJSON, want: `{"o1":15,"o2":20}`},
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
	{expr: `a.%`, data: `{"a":{"b":1}}`, want: `{"a":{"b":1}}`},
	{expr: `%`, code: "S0217"},
	{expr: `%.a`, code: "S0217"},
	{expr: `a.b.%.%.%.x`, data: `{"a":{"b":1}}`, code: "S0217"},
}

var bindingOperatorCases = []exprCase{
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
	{
		expr: `Account.Order@$o.$o.Product{%.OrderID: Name}`, data: accountJSON,
		want: `{"o1":["Hat","Cap"],"o2":"Bag"}`,
	},
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
}

var groupAndSortCases = []exprCase{
	{expr: `Account.Order.Product{Name: Price}`, data: accountJSON, want: `{"Bag":20,"Cap":5,"Hat":10}`},
	{expr: `Account.Order.{OrderID: Product.Name}`, data: accountJSON, want: `[{"o1":["Hat","Cap"]},{"o2":"Bag"}]`},
	{expr: `a{b: c}.k`, data: `{"a":[{"b":"k","c":1},{"b":"k","c":2}]}`, want: `[1,2]`},
	{expr: `a{b: c[]}`, data: `{"a":[{"b":"k","c":1}]}`, want: `{"k":[1]}`},
	{expr: `a{b: undefined}`, data: `{"a":[{"b":"k","c":1}]}`, want: `{}`},
	{expr: `a.[b,c]{"k":$}`, data: `{"a":[{"b":1,"c":2},{"b":3,"c":4}]}`, want: `{"k":[1,2,3,4]}`},
	{expr: `a.[b]{"k":$}`, data: `{"a":[{"b":1,"c":2},{"b":3,"c":4}]}`, want: `{"k":[1,3]}`},
	{expr: `a#$i.[b,c]{"k":$count($)}`, data: `{"a":[{"b":1,"c":2},{"b":3,"c":4}]}`, want: `{"k":4}`},
	{expr: `[[1,2],[3,4]]{"k":$}`, want: `{"k":[1,2,3,4]}`},
	{expr: `a{b: c, b: d}`, data: `{"a":{"b":"k","c":1,"d":2}}`, code: "D1009"},
	{expr: `a{b: $error("g")}`, data: `{"a":{"b":"k","c":1}}`, code: "D3137"},
	{expr: `a{$error("k"): 1}`, data: `{"a":{"b":"k","c":1}}`, code: "D3137"},
	{expr: `a{5: 1}`, data: `{"a":{"b":"k","c":1}}`, code: "T1003"},
	{expr: `{"a":1, "a":2}`, code: "D1009"},
	{expr: `{ $string(1): 2 }`, want: `{"1":2}`},
	{expr: `{1: 2}`, code: "T1003"},
	{expr: `[3,1,2]^($)`, want: `[1,2,3]`},
	{expr: `[{"a":"x"},{"a":1}]^(a)`, code: "T2007"},
	{expr: `[{"a":2},{"a":1}]^($error("s"))`, code: "D3137"},
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
}

var pathStepCases = []exprCase{
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
}

func TestPathAndOperatorSemantics(t *testing.T) {
	t.Run("parent operator", func(t *testing.T) { runExprCases(t, parentOperatorCases) })
	t.Run("binding operators", func(t *testing.T) { runExprCases(t, bindingOperatorCases) })
	t.Run("group and sort", func(t *testing.T) { runExprCases(t, groupAndSortCases) })
	t.Run("transform", func(t *testing.T) { runExprCases(t, transformCases) })
	t.Run("path steps", func(t *testing.T) { runExprCases(t, pathStepCases) })
	t.Run("subscripts", func(t *testing.T) { runExprCases(t, subscriptCases) })
	t.Run("operators", func(t *testing.T) { runExprCases(t, operatorCases) })
}
