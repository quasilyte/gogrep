package gogrep

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/exp/typeparams"
)

// FIXME: find test case duplicates.

func TestMatchPresetCapture(t *testing.T) {
	type captureVars = map[string]string

	tests := []struct {
		pat        string
		preset     map[string]string
		numMatches int
		input      string
	}{
		{`$x`, captureVars{"x": "10"}, 1, `10`},
		{`$x`, captureVars{"x": "5"}, 0, `10`},
		{`$x = $x`, captureVars{"x": "v"}, 1, `v = v`},
		{`$x = $x`, captureVars{"x": "v"}, 0, `v = v2`},
		{`$x = $x`, captureVars{"x": "v"}, 0, `y = y`},
	}

	for i := range tests {
		test := tests[i]
		t.Run(fmt.Sprintf("test%d", i), func(t *testing.T) {
			state := NewMatcherState()
			fset := token.NewFileSet()
			config := CompileConfig{
				Fset: fset,
				Src:  test.pat,
			}
			for k, s := range test.preset {
				n := testParseNode(t, fset, s)
				state.CapturePreset = append(state.CapturePreset, CapturedNode{
					Name: k,
					Node: n,
				})
			}
			pat, _, err := Compile(config)
			if err != nil {
				t.Fatalf("compile `%s`: %v", test.pat, err)
			}
			target := testParseNode(t, token.NewFileSet(), test.input)
			matches := 0
			testAllMatches(pat, &state, target, func(m MatchData) {
				matches++
			})
			if matches != test.numMatches {
				t.Fatalf("test `%s`:\ntarget: `%s`\nhave: %v\nwant: %v",
					test.pat, test.input, matches, test.numMatches)
			}
		})
	}
}

func TestMatchPos(t *testing.T) {
	tests := []struct {
		pat     string
		input   string
		matched string
	}{
		{
			`for $_, $v := range $xs`,
			`package p; func _() { for _, v := range elems {} }`,
			`for _, v := range elems`,
		},
		{
			`for $_, $v := range $xs`,
			`package p
				func _() {
					for _, v := range f()[:] {
						println(v)
					}
				}
			`,
			`for _, v := range f()[:]`,
		},

		{
			`range $x`,
			`package p; func _() { for range xs {} }`,
			`range xs`,
		},
		{
			`range $x`,
			`package p; func _() { for i := range xs[:] {} }`,
			`range xs[:]`,
		},
		{
			`range $x`,
			`package p; func _() { for i, v := range f() {} }`,
			`range f()`,
		},
		{
			`range $x`,
			`package p; func _() { for i = range xs[:] {} }`,
			`range xs[:]`,
		},
		{
			`range $x`,
			`package p; func _() { for i, v = range f() {} }`,
			`range f()`,
		},
	}

	for i := range tests {
		test := tests[i]
		t.Run(fmt.Sprintf("test%d", i), func(t *testing.T) {
			state := NewMatcherState()
			config := CompileConfig{Fset: token.NewFileSet(), Src: test.pat}
			pat, _, err := Compile(config)
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			target := testParseNode(t, fset, test.input)
			matched := ""
			testAllMatches(pat, &state, target, func(m MatchData) {
				from := fset.Position(m.Node.Pos()).Offset
				to := fset.Position(m.Node.End()).Offset
				matched = test.input[from:to]
			})
			if matched != test.matched {
				t.Fatalf("result mismatch:\nhave: %q\nwant: %q\npattern: %s\ninput: %s",
					matched, test.matched, test.pat, test.input)
			}
		})
	}
}

func TestMatchCapture(t *testing.T) {
	tests := []struct {
		pat     string
		input   string
		capture string
	}{
		{`import $i`, `package p; import "fmt"`, `i:"fmt"`},
		{`import $i`, `package p; import ("fmt")`, `i:"fmt"`},
		{`import $i`, `package p; import ("fmt"; "strings")`, `i:"fmt"; "strings"`},
		{`import $imports`, `package p; import ("fmt"; "strings")`, `imports:"fmt"; "strings"`},
		{`import $imports`, `package p; import (crand "crypto/rand"; "strings")`, `imports:crand "crypto/rand"; "strings"`},

		{
			`range $x`,
			`package p; func _() { for i, x := range data[0] { println(i, x) } }`,
			`x:data[0]`,
		},

		{
			`range $x`,
			`package p; func _() { for range data[0] {} }`,
			`x:data[0]`,
		},
	}

	for i := range tests {
		test := tests[i]
		t.Run(fmt.Sprintf("test%d", i), func(t *testing.T) {
			state := NewMatcherState()
			config := CompileConfig{Fset: token.NewFileSet(), Src: test.pat}
			pat, _, err := Compile(config)
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			target := testParseNode(t, fset, test.input)
			var capture []string
			testAllMatches(pat, &state, target, func(m MatchData) {
				for _, c := range m.Capture {
					from := fset.Position(c.Node.Pos()).Offset
					to := fset.Position(c.Node.End()).Offset
					capture = append(capture, c.Name+":"+test.input[from:to])
				}
			})
			have := strings.Join(capture, ", ")
			want := test.capture
			if have != want {
				t.Fatalf("capture mismatch:\nhave: %s\nwant: %s\npattern: %s\ninput: %s",
					have, want, test.pat, test.input)
			}
		})
	}
}

func TestMatchWithTypes(t *testing.T) {
	tests := []struct {
		pat        string
		numMatches int
	}{
		{`fmt.Sprintf($*_)`, 2},
		{`fmt.$_($*_)`, 2},

		{`rand.Read($_)`, 1},

		{`astequal.Expr($*_)`, 1},
	}

	fileSrc := `package example

import (
	"fmt"
	"github.com/go-toolsmith/astequal"
	crand "crypto/rand"
	"math/rand"
)

func testFunc(format string, args []interface{}) {
	_ = fmt.Sprintf("%d", 1)
	_ = fmt.Sprintf(format, args...)
	_ = astequal.Expr(nil, nil)
	_, _ = crand.Read(nil)
	_, _ = rand.Read(nil)
	_, _ = rand.Read(nil)
}

type dummy struct{}

func (dummy) Expr(x, y interface{}) bool { return false }

func (dummy) Read(b []byte) (int, error) { return 0, nil }

func _() {
	var astequal dummy
	_ = astequal.Expr(nil, nil)
	_ = astequal.Expr(1, nil)
	_ = astequal.Expr(1, 2)
}

func _() {
	var rand dummy
	_, _ = rand.Read(nil)
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "file.go", fileSrc, 0)
	if err != nil {
		t.Fatal(err)
	}
	typesInfo := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	typechecker := &types.Config{
		Importer: importer.ForCompiler(fset, "source", nil),
	}
	_, err = typechecker.Check("example", fset, []*ast.File{f}, typesInfo)
	if err != nil {
		t.Fatal(err)
	}

	customImports := map[string]string{
		"astequal": "github.com/go-toolsmith/astequal",
		"rand":     "crypto/rand",
	}

	for i := range tests {
		test := tests[i]
		t.Run(fmt.Sprintf("test%d", i), func(t *testing.T) {
			state := NewMatcherState()
			state.Types = typesInfo
			fset := token.NewFileSet()
			testPattern := test.pat
			config := CompileConfig{
				Fset:      fset,
				Src:       testPattern,
				WithTypes: true,
				Imports:   customImports,
			}
			pat, _, err := Compile(config)
			if err != nil {
				t.Errorf("compile `%s`: %v", test.pat, err)
				return
			}
			matches := 0
			testAllMatches(pat, &state, f, func(m MatchData) {
				matches++
			})
			if matches != test.numMatches {
				t.Errorf("test `%s`:\nhave: %v\nwant: %v",
					test.pat, matches, test.numMatches)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	strict := func(s string) string {
		return "STRICT " + s
	}
	isStrict := func(s string) bool {
		return strings.HasPrefix(s, "STRICT ")
	}
	unwrapPattern := func(s string) string {
		s = strings.TrimPrefix(s, "STRICT ")
		return s
	}

	type testCase struct {
		pat        string
		numMatches int
		input      string
	}

	tests := []testCase{
		{`123`, 1, `123`},
		{`123`, 0, `12`},
		{`2.71828i`, 1, `2.71828i`},
		{`2.71828i`, 0, `2.71820i`},
		{`'a'`, 1, `'a'`},
		{`'a'`, 0, `'b'`},
		{`'✓'`, 1, `'✓'`},
		{`"ab"`, 1, `"ab"`},
		{`"ab"`, 0, `"foo"`},
		{`true`, 1, `true`},
		{`false`, 0, `true`},

		{`($x)`, 1, `(a + b)`},
		{`($x)`, 0, `a + b`},

		{`$x`, 1, `123`},
		{`$_`, 1, `123`},

		{`;`, 1, `;`},
		{`;`, 0, `1`},

		// In strict mode, differently spelled literals won't match.
		{strict(`"a"`), 1, `"a"`},
		{strict("`a`"), 1, "`a`"},
		{strict(`"a"`), 0, "`a`"},
		{strict("`a`"), 0, `"a"`},
		{strict("`a`"), 0, `"b"`},
		{strict(`'✓'`), 0, `'\u2713'`},
		{strict(`'\n'`), 0, `'\x0a'`},
		{strict(`0x0`), 0, `0`},
		{strict(`2748i`), 1, `2748i`},
		{strict(`2748i`), 0, `2740i`},
		{strict(`4.5`), 1, `4.5`},
		{strict(`0.01`), 0, `.01`},

		// In non-strict mode, these literals can match.
		{`"aa"`, 1, "`aa`"},
		{`'\n'`, 1, `'\x0a'`},
		{`0x0`, 1, `0`},
		{`3`, 1, `0b11`},
		{`0.01`, 1, `.01`},
		{`'a'`, 0, `"a"`},
		{`'✓'`, 0, `10003`},
		{`'✓'`, 1, `'\u2713'`},

		// Binary op.
		{strict(`1 + 2`), 1, `1 + 2`},
		{`$_`, 3, `1 * 2`},
		{`x == y`, 1, `x == y`},
		{`x == y`, 0, `x == x`},
		{`x == y`, 0, `x != y`},
		{`x != y`, 1, `x != y`},
		{`$x + $x`, 1, `x + x`},
		{`$x + $x`, 0, `x + y`},
		{`$x + $x`, 0, `x + 0`},
		{`$x + $x`, 0, `foo(a) + foo(b)`},
		{`$x + $x`, 1, `foo(a) + foo(a)`},

		// Unary op.
		{`+x`, 1, `+x`},
		{`+x`, 0, `+y`},
		{`-someConst`, 1, `- someConst`},
		{`*someVar`, 1, `* someVar`},
		{`-someConst`, 0, `someConst`},
		{`*someVar`, 0, `someVar`},

		// Forcing node to be a statement.
		{`append($*_);`, 1, `{ f(); append(x, a) }`},
		{`append($*_);`, 0, `{ f(); x = append(x, a) }`},

		// Call expr.
		{`f(1, 2, "foo")`, 1, `f(1, 2, "foo")`},
		{`f(1, 2, "foo")`, 0, `g(1, 2, "foo")`},
		{`f(1, 2, "foo")`, 0, `f(1, 2, "bar")`},
		{`f(1, 2, "foo")`, 0, `f(1, 2)`},
		{`f(1, 2)`, 0, `f(1, 2, "foo")`},
		{`print($*x)`, 1, `print()`},
		{`print($*x)`, 1, `print(a, b)`},
		{`print($*_)`, 1, `print()`},
		{`print($*_)`, 1, `print(a, b)`},
		{`print($*x, $y, $*z)`, 0, `print()`},
		{`print($*x, $y, $*z)`, 1, `print(a)`},
		{`print($*x, $y, $*z)`, 1, `print(a, b, c)`},
		{`print($*_, x, $*_)`, 1, `print(x)`},
		{`print($*_, f(), $*_)`, 1, `print(f())`},
		{`print($*_, f(), $*_)`, 1, `print(1, f())`},
		{`print($*_, f(), $*_)`, 1, `print(1, 2, f(), 3)`},
		{`print($*_, f(), $*_)`, 1, `print(f(), 1)`},
		{`print($*_, f($*args), $*_)`, 1, `print(f())`},
		{`print($*_, f($*args), $*_)`, 1, `print(f(1))`},
		{`print($*_, f($*args), $*_)`, 1, `print(f(1, 2), 3)`},
		{`f(1, $*_, 2)`, 1, `f(1, 2)`},
		{`f(1, $*_, 2)`, 1, `f(1, "x", 2)`},
		{`f(1, $*_, 2)`, 1, `f(1, "x", "y", 2)`},
		{`f(1, $*_, 2)`, 0, `f(1, "x", "y")`},
		{`f(1, $*_, 2)`, 0, `f(1)`},
		{`foo($x, $x)`, 0, `foo(1, 2)`},
		{`foo($_, $_)`, 1, `foo(1, 2)`},
		{`foo($x, $y, $y)`, 1, `foo(1, 2, 2)`},
		{`foo($x, $y, $y)`, 0, `foo(1, 2, 3)`},
		{`$x.Method()`, 1, `a.Method()`},
		{`$x.Method()`, 1, `a.b.Method()`},
		{`$x.Method()`, 0, `a.b.Method`},
		{`$x.Method()`, 0, `a.b.Method2()`},
		{`x.Method()`, 0, `y.Method2()`},
		{`$x.Method()`, 0, `a.Method(1)`},
		{`f($*_)`, 1, `f(xs...)`},
		{`f($*_)`, 1, `f(1, xs...)`},
		{`f($*_)`, 1, `f(1, 2, xs...)`},
		{`f($_, $*_)`, 1, `f(1, 2, xs...)`},
		{`f($*_, $_)`, 0, `f(1, 2, xs...)`},
		{`f($*_, xs)`, 0, `f(1, 2, xs...)`},
		{`f($*_, xs...)`, 1, `f(1, 2, xs...)`},
		{`fmt.Sprintf($_, $_...)`, 1, `fmt.Sprintf(format, args...)`},
		{`fmt.Sprintf($_, $args...)`, 1, `fmt.Sprintf(format, args...)`},
		{`fmt.Sprintf($_, $args...)`, 1, `fmt.Sprintf(f, a...)`},
		{`fmt.Sprintf($_, $args...)`, 0, `fmt.Sprintf(f, a)`},
		{`fmt.Sprintf($_, $args...)`, 0, `fmt.Sprintf(f)`},
		{`fmt.Sprintf($_, $args...)`, 0, `fmt.Sprintf(f, a, b)`},
		{`fmt.Sprintf($_, $args)`, 0, `fmt.Sprintf(f, a...)`},
		{`$fmt.$_($*_)`, 1, `fmt.Sprintf("%d", 1)`},

		// OK: trailing $*_ can match variadic calls.
		{`f($*_)`, 1, `f(xs...)`},
		{`f($*_)`, 1, `f(1, xs...)`},
		{`f($_, $*_)`, 1, `f(1, xs...)`},
		{`f($_, $*_)`, 1, `f(1, 2, xs...)`},
		{`f($_, $_, $*_)`, 1, `f(1, 2, xs...)`},
		{`f($_, $_, $*_)`, 1, `f(1, 2, 3, xs...)`},
		{`f($*_, $_, $*_)`, 1, `f(1)`},
		// TODO: the case below should probably be rejected.
		{`f($*_, $_, $*_)`, 1, `f(xs...)`},
		// It doesn't allow to match a variadic call in the following cases.
		{`f($_, $*_)`, 0, `f(xs...)`},
		{`f($_, $_, $*_)`, 0, `f(1, xs...)`},

		// Selector expr.
		{`$x.Field`, 1, `a.Field`},
		{`$x.Field`, 1, `b.Field`},
		{`$x.Field`, 0, `a.field`},
		{`a.b`, 1, `a.b.c`},
		{`b.c`, 0, `a.b.c`},
		{`$x.c`, 1, `a.b.c`},
		{`a.$x`, 1, `a.b.c`},

		// Index expr.
		{`$x[0][1]`, 1, `x[0][1]`},
		{`$x[0][1]`, 1, `x[10][0][1]`},
		{`$x[0][1]`, 0, `x[0][10]`},
		{`$x[len($x)-1]`, 1, `a[len(a)-1]`},
		{`$x[len($x)-1]`, 0, `a[len(b)-1]`},

		// Slice expr.
		{`x[:]`, 1, `x[:]`},
		{`x[:]`, 0, `y[:]`},
		{`x[:]`, 0, `x[1:]`},
		{`x[:]`, 0, `x[:1]`},
		{`x[:y]`, 1, `x[:y]`},
		{`x[:y]`, 0, `x[:z]`},
		{`x[:y]`, 0, `z[:y]`},
		{`x[:y]`, 0, `x[:y:z]`},
		{`$x[:$y]`, 1, `a[:1]`},
		{`$x[:$y]`, 0, `a[:]`},
		{`x[y:]`, 1, `x[y:]`},
		{`x[y:]`, 0, `z[y:]`},
		{`x[y:]`, 0, `x[z:]`},
		{`$x[$y:]`, 1, `a[1:]`},
		{`$x[$y:]`, 0, `a[:]`},
		{`x[y:z]`, 1, `x[y:z]`},
		{`x[y:z]`, 0, `_[y:z]`},
		{`x[y:z]`, 0, `x[_:z]`},
		{`x[y:z]`, 0, `x[y:_]`},
		{`x[y:z]`, 0, `x[y:]`},
		{`x[:y:z]`, 1, `x[:y:z]`},
		{`x[:y:z]`, 0, `_[:y:z]`},
		{`x[:y:z]`, 0, `x[:_:z]`},
		{`x[:y:z]`, 0, `x[:y:_]`},
		{`x[:y:z]`, 0, `x[0:y:z]`},
		{`x[:y:z]`, 0, `x[:y]`},
		{`x[5:y:z]`, 1, `x[5:y:z]`},
		{`x[5:y:z]`, 0, `_[5:y:z]`},
		{`x[5:y:z]`, 0, `x[_:y:z]`},
		{`x[5:y:z]`, 0, `x[5:_:z]`},
		{`x[5:y:z]`, 0, `x[5:y:_]`},
		{`x[5:y:z]`, 0, `x[0:y:z]`},
		{`x[5:y:z]`, 0, `x[5:y]`},
		{`x[$*_:]`, 1, `x[:]`},
		{`x[$*_:]`, 1, `x[1:]`},
		{`x[$*_:]`, 0, `x[1:2]`},
		{`x[:$*_]`, 1, `x[:]`},
		{`x[:$*_]`, 1, `x[:2]`},
		{`x[:$*_]`, 0, `x[1:2]`},
		{`x[$*_:$*_]`, 1, `x[:]`},
		{`x[$*_:$*_]`, 1, `x[1:]`},
		{`x[$*_:$*_]`, 1, `x[:2]`},
		{`x[$*_:$*_]`, 1, `x[1:2]`},
		{`x[$*_:$*_]`, 0, `x[1:2:2]`},
		{`x[$*_:$*_:$*_]`, 1, `x[:]`},
		{`x[$*_:$*_:$*_]`, 1, `x[1:]`},
		{`x[$*_:$*_:$*_]`, 1, `x[:2]`},
		{`x[$*_:$*_:$*_]`, 1, `x[1:2]`},
		{`x[$*_:$*_:$*_]`, 1, `x[1:2:2]`},
		{`x[$*y:$*y]`, 1, `x[:]`},
		{`x[$*y:$*y]`, 1, `x[1:1]`},
		{`x[$*y:$*y]`, 0, `x[1:0]`},

		// Composite literals.
		{`[]int{1, $x, $x}`, 1, `[]int{1, 2, 2}`},
		{`[]int{1, $x, $x}`, 0, `[]byte{1, 2, 2}`},
		{`[]int{1, $x, $x}`, 0, `[2]int{1, 2, 2}`},
		{`[]int{1, $x, $x}`, 0, `[]int{1, 2}`},
		{`[]string{$*_}`, 1, `[]string{"x", "y"}`},
		{`[][]int{{$x, $y}}`, 1, `[][]int{{f(), 1}}`},
		{`[][]int{{$x, $y}}`, 0, `[][]int{[]int{f(), 1}}`},
		{`[][]int{[]int{$x, $y}}`, 1, `[][]int{[]int{f(), 1}}`},
		{`[][]int{[]int{$x, $y}}`, 0, `[][]int{{f(), 1}}`},
		{`[]float64{$x}`, 1, `[]float64{3}`},
		{`[2]bool{$x, 0}`, 0, `[2]bool{3, 1}`},
		{`someStruct{fld: $x}`, 0, `someStruct{fld: a, fld2: b}`},
		{`map[int]int{1: $x}`, 1, `map[int]int{1: a}`},
		{`map[int]int{1: $x}`, 0, `map[int]byte{1: a}`},

		// Type assert.
		{`$x.([]string)`, 1, `a.([]string)`},
		{`$x.(string)`, 0, `a.(int)`},
		{`$x.($_)`, 1, `a.(b)`},
		{`$x.($x)`, 1, `int.(int)`},
		{`$x.($x)`, 0, `int.([]string)`},
		{`x.(string)`, 0, `y.(string)`},

		// Type expr.
		{`[8]$x`, 1, `[8]int{4: 1}`},
		{`struct{}`, 1, `type _ struct{}`},
		{`struct{}`, 1, `struct{}{}`},
		{`struct{}`, 0, `type _ struct{x int}`},
		{`struct{field $t}`, 1, `struct{field int}{}`},
		{`struct{field $t}`, 1, `struct{field int}{}`},
		{`struct{field $t}`, 0, `struct{other int}{}`},
		{`struct{field $t}`, 0, `(struct{f1, f2 int}{})`},
		{`struct{$*_}`, 1, `struct{}{}`},
		{`struct{$*_}`, 1, `struct{x int}{}`},
		{`struct{$*_}`, 1, `struct{x, y int}{}`},
		{`struct{$*_}`, 1, `struct{x int; y float64}{}`},
		{`struct{$*_}`, 0, `1`},
		{`struct{$_ $x; $_ $x}`, 1, `struct{x int; y int}{}`},
		{`struct{$_ $x; $_ $x}`, 0, `struct{x int; y string}{}`},
		{`struct{$*_; $_ $x; $_ $x; $*_}`, 1, `struct{x int; y int}{}`},
		{`struct{$*_; $_ $x; $_ $x; $*_}`, 1, `struct{x int; y int; z string}{}`},
		{`struct{$*_; $_ $x; $_ $x; $*_}`, 0, `struct{x string; y int; z string}{}`},
		{`struct{$x; $x}`, 1, `struct{x int; x int}{}`},
		{`struct{$x; $x}`, 0, `struct{x int; y int}{}`},
		{`struct{$x; $x}`, 0, `struct{x int; x string}{}`},
		{`struct{$x; $x}`, 0, `struct{x int}{}`},
		{`struct{$_}`, 1, `struct{io.Reader}{}`},
		{`struct{$_}`, 0, `struct{}{}`},
		{`struct{$_}`, 0, `struct{x int}{}`},
		{`struct{$_ $_}`, 1, `struct{x int}{}`},
		{`struct{$_, $_ $_}`, 1, `struct{x, y int}{}`},
		{`struct{$_, $_ $_}`, 0, `struct{x int}{}`},
		{`struct{$_, $_ $_}`, 0, `struct{x int; y int}{}`},
		{`var x struct{$x}; var y $x`, 1, `{ var x struct{io.Reader}; var y io.Reader }`},
		{`var x struct{$_ $x}; var y $x`, 1, `{ var x struct{r io.Reader}; var y io.Reader }`},
		{`var x struct{$x}; var y $x`, 0, `{ var x struct{io.Writer}; var y io.Reader }`},
		{`var x struct{$_ $x}; var y $x`, 0, `{ var x struct{r io.Writer}; var y io.Reader }`},
		{`var x struct{$_ $x}; var y $x`, 0, `{ var x struct{io.Reader}; var y io.Reader }`},
		{`interface{$x() int}`, 1, `(interface{i() int})(nil)`},
		{`interface{}`, 1, `interface{}(nil)`},
		{`interface{}`, 1, `interface{}(nil)`},
		{`interface{}`, 0, `interface{io.Reader}(nil)`},
		{`interface{}`, 0, `interface{Int() int}(nil)`},
		{`interface{Int() int}`, 1, `interface{Int() int}(nil)`},
		{`interface{Int() int}`, 0, `interface{String() int}(nil)`},
		{`interface{Int() int}`, 0, `interface{Int() string}(nil)`},
		{`interface{Int() int}`, 0, `interface{}(nil)`},
		{`interface{$_($x) $x}`, 1, `interface{Foo(int) int}(nil)`},
		{`interface{$_($x) $x}`, 0, `interface{Foo() int}(nil)`},
		{`interface{$_($x) $x}`, 0, `interface{Foo(int)}(nil)`},
		{`interface{$_($x) $x}`, 0, `interface{Foo(string) int}(nil)`},
		{`interface{$*_; String() string; $*_}`, 1, `interface{String() string}(nil)`},
		{`interface{$*_; String() string; $*_}`, 1, `interface{Int() int; String() string}(nil)`},
		{`interface{$*_; String() string; $*_}`, 1, `interface{String() string; Int() int}(nil)`},
		{`interface{$*_; String() string; $*_}`, 1, `interface{Float() float64; String() string; Int() int}(nil)`},
		{`chan<- int`, 1, `make(chan<- int)`},
		{`chan<- int`, 0, `make(chan <-string)`},
		{`chan<- int`, 0, `make(chan int)`},
		{`chan<- int`, 0, `make(<-chan int)`},
		{`chan $x`, 1, `new(chan bool)`},
		{`chan $x`, 0, `(chan<- bool)(nil)`},

		// Key-value expr.
		{`"a": 1`, 1, `map[string]int{"a": 1}`},
		{`"a": 1`, 0, `map[string]int{"a": 0}`},
		{`"a": 1`, 0, `map[string]int{"b": 1}`},
		{`"a": 1`, 0, `map[string]int{}`},
		{`$x: 1`, 1, `map[string]int{"x": 1}`},
		{`$x: 1`, 1, `map[string]int{"y": 1}`},
		{`$x: 1`, 0, `map[string]int{"z": 2}`},
		{`"a": $x`, 1, `map[string]int{"a": 1}`},
		{`"a": $x`, 1, `map[string]int{"a": 2}`},
		{`"a": $x`, 0, `map[string]int{"b": 3}`},

		// Func lit.
		{`func () {}`, 1, `func () {}`},
		{`func () {}`, 0, `func () int {}`},
		{`func () {}`, 0, `func (int) {}`},
		{`func () {}`, 0, `func (x int) {}`},
		{`func () {}`, 0, `func (int, int) {}`},
		{`func (x int) {}`, 1, `func (x int) {}`},
		{`func (x int) {}`, 0, `func (y int) {}`},
		{`func (x int) {}`, 0, `func (x string) {}`},
		{`func (int) {}`, 1, `func (int) {}`},
		{`func (int) {}`, 0, `func (string) {}`},
		{`func (int) {}`, 0, `func () {}`},
		{`func (int) {}`, 0, `func (int, int) {}`},
		{`func (int) {}`, 0, `func (int) int {}`},
		{`func (int, int) {}`, 1, `func (int, int) {}`},
		{`func (int, int) {}`, 0, `func (int) {}`},
		{`func (int, int) {}`, 0, `func (x int, y int) {}`},
		{`func (int, int) {}`, 0, `func (string, int) {}`},
		{`func (int, int) {}`, 0, `func (int, string) {}`},
		{`func (int, int) {}`, 0, `func (int, int) int {}`},
		{`func (x, y int) int {}`, 1, `func (x, y int) int {}`},
		{`func (x, y int) int {}`, 0, `func (x int, y int) int {}`},
		{`func (x, y int) int {}`, 0, `func (x, y string) int {}`},
		{`func (x, y int) int {}`, 0, `func (x, y int) string {}`},
		{`func (x, y int) int {}`, 0, `func (x, y int) (int, int) {}`},
		{`func (x, y int) int {}`, 0, `func (y, x int) int {}`},
		{`func () (int, int) {}`, 1, `func () (int, int) {}`},
		{`func () (int, int) {}`, 0, `func () int {}`},
		{`func () (int, int) {}`, 0, `func () (string, int) {}`},
		{`func () (int, int) {}`, 0, `func () (x int, y int) {}`},
		{`func () int { return 1 }`, 1, `func () int { return 1 }`},
		{`func () int { return 1 }`, 0, `func () int { return 0 }`},
		{`func ($t, $t) {}`, 1, `func (int, int) {}`},
		{`func ($t, $t) {}`, 0, `func (string, int) {}`},
		{`func ($t, $t) {}`, 0, `func (int, string) {}`},
		{`func($s string) { print($s) }`, 1, `func(a string) { print(a) }`},
		{`func(x ...int) {}`, 1, `func(x ...int) {}`},
		{`func(x ...int) {}`, 0, `func(x int) {}`},
		{`func(x ...int) {}`, 0, `func(y ...int) {}`},
		{`func(x ...int) {}`, 0, `func(x ...string) {}`},
		{`func($x ...$t) {}`, 1, `func(a ...int) {}`},

		// Func lit - non-strict mode.
		// TODO: reject these in strict mode.
		{`func () (int) {}`, 1, `func () int {}`},
		{`func () int {}`, 1, `func () (int) {}`},

		// Assign stmt.
		{`$x = $y`, 1, `a = b`},
		{`x := y`, 0, `x = y`},
		{`$x := $y`, 0, `a, b := c()`},
		{`$x := $y`, 0, `a, b = c()`},
		{`$x := $y`, 0, `a, b := c, d`},
		{`$*x = $*x`, 1, `x, y = x, y`},
		{`$*x = $*x`, 0, `x, y = 0, 0`},
		{`$*_ = $*_`, 1, `x = 1`},
		{`$*_ = $*_`, 1, `x, y = 1, 2`},

		// Block stmt.
		{`{ $x }`, 1, `{ a() }`},
		{`{ $x }`, 0, `{ a(); b() }`},
		{`{}`, 1, `package p; func f() {}`},
		{`{ $*_ }`, 1, `{ x; y }`},
		{`{ x; y }`, 1, `{ x; y }`},
		{`{ x; y; z }`, 1, `{ x; y; z }`},
		{`{ x; y }`, 0, `{ y; x }`},
		{`{ x; y }`, 0, `{ x }`},
		{`{ x; f(); y; g() }`, 1, `{ x; f(); y; g() }`},
		{`{ x; $*_; g() }`, 1, `{ x; f(); y; g() }`},
		{`{ $*_; g() }`, 1, `{ x; f(); y; g() }`},
		{`{ x; $*_ }`, 1, `{ x; f(); y; g() }`},
		{`{ x; $*_ }`, 1, `{ x; y }`},
		{`{ x; $*_ }`, 0, `{ y; x }`},
		{`{ $*_; y }`, 1, `{ x; y }`},
		{`{ $*_; y }`, 0, `{ y; x }`},
		{`{ $*_; f(); $*_ }`, 1, `{ f() }`},
		{`{ $*_; f(); $*_ }`, 1, `{ g(); f() }`},
		{`{ $*_; x; $*_ }`, 1, `{ y; x; y }`},
		{`{ $*_; f(); $*_ }`, 1, `{ g(); g(); f() }`},
		{`{ $*_; f(); $*_ }`, 0, `{ g(); g() }`},
		{`{ $*_; f(); $*_ }`, 0, `{}`},
		{`{ $*_; return nil }`, 1, `{ return nil }`},
		{`{ $*_; return nil }`, 1, `{ a(); b(); return nil }`},

		// Labeled stmt.
		{`foo: if x {}`, 1, `foo: if x {}`},
		{`foo: if x {}`, 0, `foo: if y {}`},
		{`foo: if x {}`, 0, `bar: if y {}`},
		{`$label: if f() {}`, 1, `foo: if f() {}`},
		{`$label: if f() {}`, 1, `bar: if f() {}`},
		{`$l: return 1; $l: return 2`, 1, `{ x: return 1; x: return 2 }`},
		{`$l: return 1; $l: return 2`, 0, `{ x: return 1; y: return 2 }`},

		// Send stmt.
		{`x <- 1`, 1, `x <- 1`},
		{`x <- $v`, 0, `y <- 0`},
		{`x <- 1`, 0, `x <- 2`},

		// Go stmt.
		{`go f(1)`, 1, `go f(1)`},
		{`go f(1)`, 0, `go g(1)`},
		{`go func() { $x }()`, 1, `go func() { a() }()`},
		{`go func() { $x }()`, 0, `go a()`},

		// Defer stmt.
		{`defer f(1)`, 1, `defer f(1)`},
		{`defer f(1)`, 0, `defer g(1)`},
		{`defer func() { $x }()`, 1, `defer func() { a() }()`},
		{`defer func() { $x }()`, 0, `defer a()`},

		// If stmt.
		{`if $x != nil { $y }`, 1, `if p != nil { p.foo() }`},
		{`if $x { $y }`, 0, `if a { b() } else { c() }`},
		{`if $x != nil { $y }`, 1, `if a != nil { return a }`},
		{`if $_; cond {}`, 0, `if cond {}`},
		{`if $_; cond {}`, 1, `if init; cond {}`},
		{`if $x; cond {}`, 0, `if cond {}`},
		{`if $x; cond {}`, 1, `if init; cond {}`},
		{`if $x {} else if $x {}`, 1, `if cond {} else if cond {}`},
		{`if $x {} else if $x {}`, 0, `if cond {} else if cond2 {}`},
		{`if $x {} else if $x {}`, 0, `if cond {} else {}`},
		{`if $x {} else if $x {}`, 0, `if cond { f() } else {}`},
		{`if $x {} else if $x {}`, 0, `if cond {} else { f() }`},
		{`if $x {} else if $x {}`, 0, `if cond {} else if cond { f() }`},
		{
			`if len($xs) != 0 { for _, $x = range $xs { $*_ } }`,
			1,
			`if len(xs) != 0 { for _, v = range xs { println(v) } }`,
		},
		{
			`if len($xs) != 0 { for _, $x := range $xs { $*_ } }`,
			0,
			`if len(xs) != 0 { for _, v = range xs { println(v) } }`,
		},

		// If stmt - optional matching.
		{`if $*_; cond {}`, 1, `if cond {}`},
		{`if $*_; cond {}`, 1, `if init; cond {}`},
		{`if $*x; cond {}`, 1, `if cond {}`},
		{`if $*x; cond {}`, 1, `if init; cond {}`},
		{`if $*_ {}`, 1, `if cond {}`},
		{`if $*_ {}`, 1, `if init; cond {}`},
		{`if $*x {}; if $*x {}`, 1, `for cond() { if a(); b {}; if a(); b {} }`},
		{`if $*x {}; if $*x {}`, 1, `{ if a {}; if a {} }`},
		{`if $*x {}; if $*x {}`, 0, `{ if a {}; if b {} }`},
		{`if $*x {}; if $*x {}`, 0, `for cond() { if a(); b {}; if b {} }`},
		{`if $*x {} else if $*x {}`, 1, `{ if a {} else if a {} }`},
		{`if $*x {} else if $*x {}`, 0, `{ if a {} else if b {} }`},
		{`if $*_ {} else {}`, 0, `if a(); b {}`},
		{`if $*_; $_ {} else {}`, 1, `if a() {} else {}`},
		{`if $*_; $_ {} else {}`, 1, `if a(); b {} else {}`},
		{`if $*_ {} else {}`, 1, `if a(); b {} else {}`},
		{`if $*_ {} else {}`, 1, `if a() {} else {}`},
		{`if a(); $*_ {}`, 0, `if b {}`},
		{`if $_ { $*_ }`, 1, `if cond {}`},
		{`if $_ { $*_ }`, 1, `if cond { f() }`},

		// Switch stmt.
		{`switch {}`, 1, `switch {}`},
		{`switch {}`, 0, `switch tag {}`},
		{`switch tag {}`, 1, `switch tag {}`},
		{`switch tag {}`, 0, `switch {}`},
		{`switch tag {}`, 0, `switch init; tag {}`},
		{`switch tag {}`, 0, `switch tag2 {}`},
		{`switch init; {}`, 1, `switch init; {}`},
		{`switch init; {}`, 0, `switch init2; {}`},
		{`switch init; {}`, 0, `switch init; tag {}`},
		{`switch init; {}`, 0, `switch {}`},
		{`switch init; tag {}`, 1, `switch init; tag {}`},
		{`switch init; tag {}`, 0, `switch init2; tag {}`},
		{`switch init; tag {}`, 0, `switch init; tag2 {}`},
		{`switch init; tag {}`, 0, `switch {}`},
		{`switch init; tag {}`, 0, `switch ; tag {}`},
		{`switch init; tag {}`, 0, `switch init; {}`},
		{`switch $_ {}`, 1, `switch x {}`},
		{`switch $_ {}`, 0, `switch x; y {}`},
		{`switch $_; $_ {}`, 0, `switch x {}`},
		{`switch $_; $_ {}`, 1, `switch x; y {}`},
		{`switch { $*_; case x: y() }`, 1, `switch { case x: y() }`},
		{`switch { $*_; case x: y() }`, 1, `switch { case v: g(); case x: y() }`},
		{`switch { $*_; case $*_: $*a }`, 1, `switch { case x: y() }`},
		{`switch x {case 4: x}`, 1, `switch x {case 4: x}`},
		{`switch x {case 4: x}`, 0, `switch y {case 4: x}`},
		{`switch x {case 4: x}`, 0, `switch x {case 5: x}`},
		{`switch {$_}`, 1, `switch {case 5: x}`},
		{`switch x {$_}`, 1, `switch x {case 5: x}`},
		{`switch x {$*_}`, 1, `switch x {case 5: x}`},
		{`switch x {$*_}`, 1, `switch x {}`},
		{`switch x {$*_}`, 1, `switch x {case 1: a; case 2: b}`},
		{`switch {$a; $a}`, 1, `switch {case true: a; case true: a}`},
		{`switch {$a; $a}`, 0, `switch {case true: a; case true: b}`},
		{`switch x {default: f()}`, 1, `switch x {default: f()}`},
		{`switch x {default: f()}`, 0, `switch x {default: g()}`},
		{`switch x {default: f()}`, 0, `switch x {case 10: f()}`},
		{`switch x := y.(z); x {}`, 1, `switch x := y.(z); x {}`},
		{`switch x := y.(z); x {}`, 0, `switch y := y.(z); x {}`},
		{`switch x := y.(z); x {}`, 0, `switch x {}`},
		{`switch x {case x, y: f(x, y)}`, 1, `switch x {case x, y: f(x, y)}`},
		{`switch x {case x, y: f(x, y)}`, 0, `switch x {case x: f(x, y)}`},
		{`switch x {case x, y: f(x, y)}`, 0, `switch x {case x, _: f(x, y)}`},
		{`switch x {case x, y: f(x, y)}`, 0, `switch x {case x, y: f(x, _)}`},

		// Switch stmt - optional matching.
		{`switch $*x {}`, 1, `switch a {}`},
		{`switch $*x {}`, 1, `switch {}`},
		{`switch $*_; b {}`, 1, `switch b := f(); b {}`},
		{`switch $*_; b {}`, 0, `switch b := f(); c {}`},
		// TODO {`switch $*x {}`, 1, `switch a(); b {}`},
		// TODO {`switch $*x {}; switch $*x {}`, 1, `{ switch a(); b {}; switch a(); b {} }`},
		{`switch $*x {}; switch $*x {}`, 0, `{ switch a(); b {}; switch b {} }`},
		{`switch a(); $*_ {}`, 0, `for b {}`},

		// Type switch stmt.
		{`switch x := $x.(type) {}`, 1, `switch x := y.(type) {}`},
		{`switch x := $x.(type) {}`, 1, `switch x := xs[0].(type) {}`},
		{`switch $x := $x.(type) {}`, 1, `switch x := x.(type) {}`},
		{`switch $x := $x.(type) {}`, 0, `switch y := x.(type) {}`},
		{`switch $x.(type) {}`, 1, `switch v.(type) {}`},
		{`switch $*_; x.(type) {}`, 1, `switch x.(type) {}`},
		{`switch $*_; x.(type) {}`, 1, `switch init(); x.(type) {}`},
		{`switch $*_; x.(type) {}`, 0, `switch y.(type) {}`},

		// Select stmt.
		{`select {$*_}`, 1, `select {case <-x: a}`},
		{`select {$*_}`, 1, `select {}`},
		{`select {$a; $a}`, 1, `select {case <-x: a; case <-x: a}`},
		{`select {$a; $a}`, 0, `select {case <-x: a; case <-x: b}`},
		{`select {case x := <-y: f(x)}`, 1, `select {case x := <-y: f(x)}`},
		{`select {default: f()}`, 1, `select {default: f()}`},
		{`select {default: f()}`, 0, `select {default: g()}`},
		{`select {default: f()}`, 0, `select {}`},
		{`select {default: f()}`, 0, `select {case <-x: f()}`},

		// For stmt.
		{`for {}`, 1, `for {}`},
		{`for {}`, 0, `for cond {}`},
		{`for {}`, 0, `for ; ; post {}`},
		{`for {}`, 0, `for { f() }`},
		{`for ; ; post {}`, 1, `for ; ; post {}`},
		{`for ; ; post {}`, 0, `for ; ; _ {}`},
		{`for ; ; post {}`, 0, `for {}`},
		{`for ; ; post {}`, 0, `for init; ; {}`},
		{`for ; ; post {}`, 0, `for ; cond; post {}`},
		{`for ; ; post {}`, 0, `for init; cond; {}`},
		{`for ; cond; {}`, 1, `for ; cond; {}`},
		{`for ; cond; {}`, 0, `for ; _; {}`},
		{`for ; cond; {}`, 0, `for {}`},
		{`for ; cond; {}`, 0, `for init; cond; {}`},
		{`for ; cond; {}`, 0, `for ; cond; post {}`},
		{`for ; cond; post {}`, 1, `for ; cond; post {}`},
		{`for ; cond; post {}`, 0, `for ; _; post {}`},
		{`for ; cond; post {}`, 0, `for ; cond; _ {}`},
		{`for ; cond; post {}`, 0, `for {}`},
		{`for ; cond; post {}`, 0, `for ; ; post {}`},
		{`for ; cond; post {}`, 0, `for ; _; post {}`},
		{`for ; cond; post {}`, 0, `for init; cond; post {}`},
		{`for init; ; {}`, 1, `for init; ; {}`},
		{`for init; ; {}`, 0, `for _; ; {}`},
		{`for init; ; {}`, 0, `for {}`},
		{`for init; ; {}`, 0, `for init; cond; {}`},
		{`for init; ; {}`, 0, `for init; ; post {}`},
		{`for init; ; post {}`, 1, `for init; ; post {}`},
		{`for init; ; post {}`, 0, `for {}`},
		{`for init; ; post {}`, 0, `for init; ; _ {}`},
		{`for init; ; post {}`, 0, `for _; ; post {}`},
		{`for init; ; post {}`, 0, `for init; cond; post {}`},
		{`for init; cond; {}`, 1, `for init; cond; {}`},
		{`for init; cond; {}`, 0, `for init; cond; post {}`},
		{`for init; cond; post {}`, 1, `for init; cond; post {}`},
		{`for init; cond; post {}`, 0, `for init; ; post {}`},
		{`for init; cond; post {}`, 0, `for _; cond; post {}`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 1, `for { var re = regexp.MustCompile("\\d+") }`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 1, `for { f(); var re = regexp.MustCompile("\\d+") }`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 1, `for { var re = regexp.MustCompile("\\d+"); f() }`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 1, `for { var (re = regexp.MustCompile("\\d+")) }`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 1, `for { f(); var isNumberRE = regexp.MustCompile("\\d+"); f() }`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 1, `for { f(); var isNumberRE = regexp.MustCompile("\\d+"); f(); g() }`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 1, `for { f(); g(); var isNumberRE = regexp.MustCompile("\\d+"); f() }`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 0, `for { var re, err = regexp.Compile("\\d+") }`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 0, `for {}`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 0, `for { regexp.MustCompile(pattern) }`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 0, `for { re := regexp.MustCompile(pattern) }`},
		{`for { $*_; var $_ = regexp.MustCompile($s); $*_ }`, 0, `for { re = regexp.MustCompile(pattern) }`},

		// For stmt - optional matching.
		{`for $*_; $*_; $*_ {}`, 1, `for {}`},
		{`for $*_; $*_; $*_ {}`, 1, `for ; init; {}`},
		{`for $*x; $*_; $*x {}`, 1, `for f(); cond; f() {}`},
		{`for $*x; $*_; $*x {}`, 1, `for ; cond; {}`},
		{`for $*x; $*_; $*x {}`, 0, `for f(); cond; g() {}`},
		{`for $*x; $*_; $*x {}`, 0, `for f(); cond; {}`},
		{`for $*x; $*_; $*x {}`, 0, `for ; cond; g() {}`},

		// For stmt - unintentional matches (see https://github.com/golang/go/issues/44257).
		{`for {}`, 1, `for ;; {}`},
		{`for cond {}`, 1, `for ; cond; {}`},
		{`for ;; {}`, 1, `for {}`},
		{`for ; cond; {}`, 1, `for cond {}`},

		// Range stmt.
		{`for range xs {}`, 1, `for range xs {}`},
		{`for range xs {}`, 0, `for range xs { f() }`},
		{`for range xs {}`, 0, `for range ys {}`},
		{`for range xs {}`, 0, `for i := range xs {}`},
		{`for range xs {}`, 0, `for i, x := range xs {}`},
		{`for i := range xs {}`, 1, `for i := range xs {}`},
		{`for i := range xs {}`, 0, `for i = range xs {}`},
		{`for i := range xs {}`, 0, `for i := range ys {}`},
		{`for i := range xs {}`, 0, `for j := range xs {}`},
		{`for i := range xs {}`, 0, `for range xs {}`},
		{`for i, x := range xs {}`, 1, `for i, x := range xs {}`},
		{`for i, x := range xs {}`, 0, `for i, x = range xs {}`},
		{`for i, x := range xs {}`, 0, `for j, x := range xs {}`},
		{`for i, x := range xs {}`, 0, `for i, y := range xs {}`},
		{`for i, x := range xs {}`, 0, `for i, x := range ys {}`},
		{`for i, x := range xs {}`, 0, `for range xs {}`},
		{`for $x := range $y { $z }`, 1, `for i := range l { c() }`},
		{`for $x := range $y { $z }`, 0, `for i = range l { c() }`},
		{`for $x = range $y { $z }`, 0, `for i := range l { c() }`},
		{`for range $y { $z }`, 0, `for _, e := range l { e() }`},

		// Range stmt - optional matching.
		{`for $*x; b; $*x {}`, 1, `for b {}`},
		{`for $*x; b; $*x {}`, 1, `for a(); b; a() {}`},
		{`for $*x; b; $*x {}`, 0, `for a(); b; c() {}`},
		// TODO:
		// {`for $*_ := range $_ {}`, 1, `for i := range xs {}`},
		// {`for $*_ = range $_ {}`, 1, `for i = range xs {}`},
		// {`for $*_ := range $_ {}`, 1, `for i, x := range xs {}`},
		// {`for $*_ = range $_ {}`, 1, `for i, x = range xs {}`},
		// {`for $*_ := range $_ {}`, 0, `for i, x = range xs {}`},
		// {`for $*_ := range $_ {}`, 0, `for {}`},
		// {`for $*_ := range $_ {}`, 0, `for cond {}`},
		// TODO:
		// {`for $*_ range $_ {}`, 1, `for range xs {}`},

		// Any for loop.
		// TODO: need to solve https://github.com/golang/go/issues/44257 first.
		// {`for $*_ {}`, 1, `for {}`},
		// {`for $*_ {}`, 1, `for i := 0; i < 10; i++ {}`},
		// {`for $*_ {}`, 1, `for range xs {}`},
		// {`for $*x {}; for $*x {}`, 1, `{ for {}; for {} }`},
		// {`for $*x {}; for $*x {}`, 1, `{ for {}; for cond {} }`},
		// {`for $*x {}; for $*x {}`, 1, `{ for x {}; for x {} }`},
		// {`for $*x {}; for $*x {}`, 0, `{ for x {}; for y {} }`},
		// {`for $*x {}; for $*x {}`, 1, `{ for range xs {}; for range xs {} }`},
		// {`for $*x {}; for $*x {}`, 0, `{ for range xs {}; for range ys {} }`},
		// {`for $*x {}; for $*x {}`, 1, `{ for a(); b(); {}; for a(); b(); {} }`},
		// {`for $*x {}; for $*x {}`, 0, `{ for a(); b(); {}; for a(); b(); c() {} }`},

		// Range clause.
		{`range xs`, 1, `for i := range xs {}`},
		{`range xs`, 1, `for i := range xs { f(i) }`},
		{`range $x`, 1, `for i := range x { f(i) }`},
		{`range $x`, 1, `for i := range y { f(i) }`},
		{`range $x`, 2, `for _, xs := range a { for _, x := range xs { println(x) } }`},
		{`range (xs)`, 1, `for i := range (xs) {}`},
		{`range xs`, 0, `for i := range ys { f(i) }`},
		{`range (xs)`, 0, `for i := range xs {}`},

		// Range header.
		{`for range $x`, 1, `for range xs {}`},
		{`for range $x`, 0, `for i := range xs {}`},
		{`for range $x`, 0, `for i = range xs {}`},
		{`for $i := range $xs`, 1, `for i := range data {}`},
		{`for $i := range $xs`, 0, `for i = range data {}`},
		{`for $i := range $xs`, 0, `for i, x := range data {}`},
		{`for $_, $i := range $xs`, 1, `for _, v := range data {}`},
		{`for $_, $i := range $xs`, 1, `for i, v := range data {}`},
		{`for $_, $i := range $xs`, 0, `for i = range data {}`},
		{`for $_, $i := range $xs`, 0, `for range data {}`},

		// Mixing expr and stmt lists.
		{`$x, $y`, 0, `{ 1; 2 }`},
		{`$x; $y`, 0, `f(1, 2)`},

		// Stmt list (+ partial matches).
		{`$*x; b; $*y`, 1, `{ a; b; c }`},
		{`$*x; b; $*x`, 0, `{ a; b; c }`},
		{`x; y`, 1, `{ x; y; z }`},
		{`x; y`, 1, `{ z; x; y }`},
		{`f(1); g(2)`, 1, `{ z; f(1); g(2); z }`},
		{`x; y`, 0, `{ x; z; y }`},
		{`x; y`, 0, `{ y; x; z }`},
		{`x; y`, 0, `{ x }`},
		{`f(g(1), 2, 3); $*_; $x; $x`, 1, `{ f(g(1), 2, 3); g(); g() }`},
		{`f(g(1), 2, 3); $*_; $x; $x`, 1, `{ f(g(1), 2, 3); g(); g(); g() }`},
		{`f(g(1), 2, 3); $*_; $x; $x`, 1, `{ f(g(1), 2, 3); f(); g(); g() }`},
		{`f(g(1), 2, 3); $*_; $x; $x`, 1, `{ f(g(1), 2, 3); f(); g(); g(); f() }`},
		{`f(g(1), 2, 3); $*_; $x; $x`, 0, `{ f(g(1), 2, 3); f(); g(); f() }`},
		{`x.a(); x.b()`, 2, `if cond { x.a(); x.b(); f(); x.a(); x.b() }`},
		{`x; x`, 1, `{ x; x; x }`},
		{`$x(); $y()`, 1, `{ a(); b() }`},
		{`$x(); $y()`, 0, `{ a() }`},
		{`$x++; $x--`, 1, `{ n; a++; b++; b-- }`},
		{`$*_; b; $*_`, 1, `{a; b; c; d}`},
		{`c($*x); c($*x)`, 1, `{ c(); c() }`},
		{`c($*x); c()`, 1, `{ c(); c() }`},
		{`c($*x); c($*x)`, 0, `if cond { c(x); c(y) }`},
		{`c($*x); c($*x)`, 0, `if cond { c(x, y); c(z) }`},
		{`c($*x); c($*x)`, 1, `if cond { c(x, y); c(x, y) }`},
		{`c($*x, y); c($*x, y)`, 1, `if cond { c(x, y); c(x, y) }`},
		{`c($*x, $*y); c($*x, $*y)`, 1, `{ c(x, y); c(x, y) }`},
		{`$x := $_; $x = $_`, 1, `{ a := n; b := n; b = m }`},
		{`$x := $_; $*_; $x = $_`, 1, `{ a := n; b := n; b = m }`},
		{`var x int; if true { f() }`, 1, `{ var x int; if true { f() } }`},
		{`var $v $_; if true { $_ }`, 1, `{ var x int; if true { x = 10 } }`},
		{`var $v $_; if $*_ { $_ }`, 1, `{ var x int; if true { x = 10 } }`},
		{`var $v $_; if $cond { $_ }`, 1, `{ var x int; if true { x = 10 } }`},
		{`var $v $_; if $cond { $v = $x }`, 1, `{ var x int; if true { x = 10 } }`},
		{`var $v $_; if $cond { $v = $x } else { $v = $y }`, 1, `{ var x int; if true { x = 10 } else { x = 20 } }`},
		{`f(); if $ok { $*_; }`, 1, `{ f(); if ok {} }`},
		{`$_, $ok := $m.Load($k); if $ok {}`, 1, `{ v, ok := m.Load(k); if ok {} }`},
		{`$_, $ok := $m.Load($k); if $ok { $*_ }`, 1, `{ v, ok := m.Load(k); if ok {} }`},
		{`$_, $ok := $m.Load($k); if $ok { $*_ }`, 1, `{ v, ok := m.Load(k); if ok { f() } }`},
		{`$_, $ok := $m.Load($k); if $ok { $*_ }`, 0, `{ v, ok1 := m.Load(k); if ok2 { f() } }`},
		{`$_, $ok := $m.Load($k); if $ok { $*_ }`, 0, `{ v, ok := m.Load(k) }`},

		// Expr list (+ partial matches).
		{`b, c`, 1, `[]int{a, b, c, d}`},
		{`b, c`, 1, `foo(a, b, c, d)`},
		{`x, x`, 1, `f(x, x)`},
		{`x, x`, 1, `f(_, x, x)`},
		{`x, x`, 1, `f(x, x, _)`},
		{`x, x`, 1, `f(_, x, x, _)`},
		{`$x, $y`, 1, `foo(1, 2)`},
		{`2, 3`, 1, `foo(1, 2, 3)`},
		{`$x, $x, $x`, 1, `foo(1, 1, 1)`},
		{`$x, $x, $x`, 1, `foo(2, 1, 1, 1)`},
		{`$x, $x, $x`, 1, `foo(1, 1, 1, 2)`},
		{`$x, $x, $x`, 1, `foo(2, 1, 1, 1, 2)`},
		{`$x, $x, $x`, 1, `[]int{1, 1, 1, 2}`},
		{`$x, $x, $x`, 1, `[]int{2, 1, 1, 1}`},
		{`$x, $x, $x`, 1, `[]int{2, 1, 1, 1, 2}`},
		{`$x, $y`, 0, `1`},
		{`$x`, 5, `[]string{a, b}`},
		{`$x, $x`, 1, `return 1, 1`},
		{`$x, $x`, 1, `return 0, 1, 0, 1, 1`},
		{`$x, $x`, 1, `return 0, 1, 1, 0`},

		// Inc/dec stmt.
		{`x++`, 1, `x++`},
		{`x++`, 0, `y++`},
		{`$x++`, 1, `a[b]++`},
		{`$x--`, 0, `a++`},

		// Return stmt.
		{`return`, 1, `return`},
		{`return`, 0, `return 1`},
		{`return nil, $x`, 1, `{ return nil, err }`},
		{`return nil, $x`, 0, `{ return nil, 0, err }`},
		{`return $*_, err, $*_`, 1, `return err`},
		{`return $*_, err, $*_`, 1, `return 1, err`},
		{`return $*_, err, $*_`, 1, `return 1, err, 2`},
		{`return $*_, err, $*_`, 1, `return 1, 2, err`},
		{`return $*_, err, $*_`, 0, `return 1, 2`},
		{`return $*_, err, $*_`, 0, `return`},

		// Branch stmt.
		{`break foo`, 1, `break foo`},
		{`break foo`, 0, `break`},
		{`break foo`, 0, `break bar`},
		{`break foo`, 0, `continue foo`},
		{`break foo`, 0, `break`},
		{`break`, 1, `break`},
		{`break`, 0, `break foo`},
		{`goto foo`, 1, `goto foo`},
		{`goto foo`, 0, `goto bar`},
		{`fallthrough`, 1, `fallthrough`},
		{`fallthrough`, 0, `break`},
		{`continue`, 1, `continue`},
		{`continue`, 0, `continue foo`},
		{`continue`, 0, `break`},
		{`break $x`, 1, `break foo`},
		{`break $x`, 0, `break`},
		{`break $x; break $x`, 1, `{ break foo; break foo }`},
		{`break $x; break $x`, 0, `{ break foo; break bar }`},

		// Ellipsis.
		{`append(xs, ys...)`, 1, `append(xs, ys...)`},
		{`append(xs, ys...)`, 0, `append(xs, ys)`},
		{`append($x, $y...)`, 1, `append(a, bs...)`},
		{`append($b[$i], append([]$_{$_}, $b[$i:]...)...)`, 1, `append(b[i], append([]int{x}, b[i:]...)...)`},
		{`append($b[$i], append([]$_{$_}, $b[$i:]...)...)`, 1, `append(b2[j], append([]int{10}, b2[j:]...)...)`},
		{`append($b[$i], append([]$_{$_}, $b[$i:]...)...)`, 0, `append(b1[i], append([]int{x}, b2[i:]...)...)`},
		{`append($b[$i], append([]$_{$_}, $b[$i:]...)...)`, 0, `append(b[i1], append([]int{x}, b[i2:]...)...)`},
		{`append($b[$i], append([]$_{$_}, $b[$i:]...)...)`, 0, `append(b2[j], append([]int{10}, b2[j:]...))`},
		{`append($b[$i], append([]$_{$_}, $b[$i:]...)...)`, 0, `append(b2[j], append([]int{10}, b2[j:])...)`},
		{`append($b[$i], append([]$_{$_}, $b[$i:]...)...)`, 0, `append(b2[j], append([]int{10}, b2[j:]))`},
		{`append($b[:$i], append($_, $b[$i:]...)...)`, 1, `append(b[:i], append(el, b[i:]...)...)`},
		{`append($b[:$i], append($_, $b[$i:]...)...)`, 0, `append(b[:i], append(el, b[i:]...))`},
		{`append($b[:$i], append($_, $b[$i:]...)...)`, 0, `append(b[:i], append(el, b[i:])...)`},
		{`append($b[:$i], append($_, $b[$i:]...)...)`, 0, `append(b[:i], append(el, b[i:]))`},
		{`append($b[:$i], append($_, $b[$i:]...)...)`, 0, `append(b[:i1], append(el, b[i:]...)...)`},
		{`append($b[:$i], append($_, $b[$i:]...)...)`, 0, `append(b[:i], append(el, b[i1:]...)...)`},
		{`append($b[:$i], append($_, $b[$i:]...)...)`, 0, `append(b1[:i], append(el, b[i1:]...)...)`},
		{`append($b[:$i], append($_, $b[$i:]...)...)`, 0, `append(b[:i], append(el, b2[i1:]...)...)`},
		{`append($b[:$i], append($_, $b[$i:]...)...)`, 0, `append(b[:i], append(el, b2[i1:])...)`},
		{`append($b[:$i], append($_, $b[$i:]...)...)`, 0, `append(b[:i], append(el, b2[i1:]...))`},
		{`foo($x...)`, 0, `foo(a)`},
		{`foo($x...)`, 0, `foo(a, b)`},
		{`foo($x)`, 0, `foo(x...)`},
		{`[...]int{}`, 1, `[...]int{}`},
		{`[...]int{}`, 0, `[1]int{}`},
		{`[1]int{}`, 0, `[...]int{}`},
		{`[$_]int{}`, 1, `[...]int{}`},

		// Func decl.
		{`func f() {}`, 1, `package p; func f() {}`},
		{`func f() {}`, 0, `package p; func (object) f() {}`},
		{`func (object) f() {}`, 1, `package p; func (object) f() {}`},
		{`func (object) f() {}`, 0, `package p; func f() {}`},
		{`func (object) f() {}`, 0, `package p; func (foo) f() {}`},
		{`func (object) f() {}`, 0, `package p; func (o object) f() {}`},
		{`func f() int`, 1, `package p; func f() int`},
		{`func f() int`, 0, `package p; func f() int {}`},
		{`func (object) f() int`, 1, `package p; func (object) f() int`},
		{`func (object) f() int`, 0, `package p; func (object) f() int {}`},
		{
			`func $_($x $y) $y { return $x }`,
			1,
			`package p; func a(i int) int { return i }`,
		},
		{`func $x(i int)`, 1, `package p; func a(i int)`},
		{`func $x(i int) {}`, 0, `package p; func b(i int)`},
		{`func $_() $*_ { $*_ }`, 1, `package p; func f() { return 0 }`},
		{`func $_() $*_ { $*_ }`, 1, `package p; func f() int { return 0 }`},
		{`func $_() $*_ { $*_ }`, 1, `package p; func f() (int, string) { return 0, "" }`},
		{`func $_() $*_ { $*_ }`, 1, `package p; func f() (a int, b string) { return }`},
		{`func $_() $*_ { $*_ }`, 1, `package p; func f() (int, error) { return 3, nil }`},
		{`func $_($*_) $x { return $x(0) }`, 1, `package p; func f() int { return int(0) }`},
		{`func $_($*_) $x { return $x(0) }`, 0, `package p; func f() int { return uint(0) }`},
		{`func $_($*_) $_ { $*_ }`, 0, `package p; func f() {}`},
		{`func $_($*_) $_ { $*_ }`, 0, `package p; func f() (int, string) { return }`},
		{`func $_($*_) $_ { $*_ }`, 0, `package p; func f() (x int) { return 0 }`},
		{`func $_($*_) ($_ $_) { $*_ }`, 1, `package p; func f() (x int) { return 0 }`},
		{`func $_($*_) $_ { $*_ }`, 1, `package p; func f() int { return 0 }`},
		{`func ($x $y) $f($*_) { $*_ }`, 1, `package p; func (t *MyType) methodName() {}`},
		{`func ($x $y) $f($*_) $*_ { $*_ }`, 1, `package p; func (t *MyType) methodName() (int, int) { return 0, 0 }`},
		{`func $_($_ $_) {}`, 1, `package p; func f(x int) {}`},
		{`func $_($_ $_, $_ $_) {}`, 1, `package p; func f(x int, y int) {}`},
		{`func $_($_, $_ $_) {}`, 1, `package p; func f(x, y int) {}`},
		{`func $_($_) {}`, 0, `package p; func f(x int) {}`},
		{`func $_($_) {}`, 0, `package p; func f(x int, y int) {}`},
		{`func $_($_) {}`, 0, `package p; func f(x, y int) {}`},
		{`func $_($_) {}`, 0, `package p; func f() {}`},

		// Gen decl.
		{`const $_ $*_ = iota`, 1, `const foo = iota`},
		{`const $_ $*_ = iota`, 1, `const foo int = iota`},
		{`const $_ $*_ = iota`, 1, `const (foo = iota)`},
		{`const $_ $*_ = iota`, 1, `const (foo int = iota)`},
		{`const $_ $*_ = iota`, 0, `const foo int = 0`},
		{`const $_ $*_ = iota`, 0, `var foo = iota`},
		{`const $_ $_ = iota`, 1, `const foo int = iota`},
		{`const $_ $_ = iota`, 0, `const foo = iota`},
		{`const $x = $y`, 1, `const a = b`},
		{`const $x = $y`, 1, `const (a = b)`},
		{`const $x = $y`, 0, "const (a = b\nc = d)"},
		{`const (x = 1; y = 2)`, 1, `const (x = 1; y = 2)`},
		{`const (x = 1; y = 2)`, 0, `const (x = 1; y = 1)`},
		{`const (x = 1; y = 2)`, 0, `const (y = 1; x = 1)`},
		{`const (x = iota; y)`, 1, `const (x = iota; y)`},
		{`const ($_ $_ = iota; $*_)`, 1, `{ const (x int = iota) }`},
		{`const ($_ $_ = iota; $*_)`, 1, `{ const (x int = iota; y) }`},
		{`const ($_ $_ = iota; $*_)`, 1, `{ const (x int = iota; y; z) }`},
		{`const ($_ $_ = iota; $_)`, 1, `{ const (x int = iota; y) }`},
		{`const ($_ $_ = iota; $_)`, 0, `{ const (x int = iota) }`},
		{`const ($_ $_ = iota; $_)`, 0, `{ const (x int = iota; y; z) }`},
		{`var x $_`, 1, `var x int`},
		{`var x $_`, 0, `var y int`},
		{`var $x int`, 1, `var a int`},
		{`var $x int`, 0, `var a int = 3`},
		{`var ()`, 1, `var()`},
		{`var ()`, 0, `var(x int)`},
		{`type x int`, 1, `type x int`},
		{`type x int`, 0, `type _ int`},
		{`type x int`, 0, `type x _`},
		{`type x int`, 0, `type x = int`},
		{`type x = int`, 1, `type x = int`},
		{`type x = int`, 0, `type _ = int`},
		{`type x = int`, 0, `type x = _`},
		{`type x = int`, 0, `type x int`},
		{`type $x int`, 1, `type foo int`},
		{`type x $t`, 1, `type x string`},
		{`type ()`, 1, `type ()`},
		{`type ()`, 0, `type (x int)`},
		{`type ()`, 0, `type x int`},
		{`type $_ struct{$*_}`, 1, `type foo struct{}`},
		{`type $_ struct{$*_}`, 1, `type foo struct{x int}`},
		{`type $_ struct{$*_}`, 0, `type foo int`},
		{`type $_ struct{$*_; Foo; $*_}`, 1, `type foo struct{Foo}`},
		{`type $_ struct{$*_; Foo; $*_}`, 1, `type foo struct{x int; Foo}`},
		{`type $_ struct{$*_; Foo; $*_}`, 1, `type foo struct{Foo; x int}`},
		{`type $_ struct{$*_; Foo; $*_}`, 1, `type foo struct{x int; Foo; y int}`},
		{`type $_ struct{$*_; Foo; $*_}`, 0, `type foo struct{*Foo}`},
		{`type $_ struct{$*_; Foo; $*_}`, 0, `type foo struct{x int}`},

		// Decl stmt.
		{`{ $*_; type $_ int; $*_ }`, 1, `{ type x int }`},
		{`{ $*_; type $_ int; $*_ }`, 1, `{ f(); type x int }`},
		{`{ $*_; type $_ int; $*_ }`, 1, `{ type x int; f() }`},
		{`{ $*_; type $_ int; $*_ }`, 1, `{ f(); type x int; f() }`},
		{`{ $*_; type $_ int; $*_ }`, 1, `{ f(); g(); type x int; f() }`},
		{`{ $*_; type $_ int; $*_ }`, 1, `{ f(); g(); type (x int); f() }`},
		{`{ $*_; type $_ int; $*_ }`, 1, `{ type x int; type y int }`},
		{`{ $*_; type $_ int; $*_ }`, 0, `{ type (y string; x int) }`},
		{`{ $*_; type $_ int; $*_ }`, 0, `{ type x string }`},
		{`{ $*_; type $_ int; $*_ }`, 0, `{ var x string }`},
		{`{ $*_; var $_ int; $*_ }`, 1, `{ var x int }`},
		{`{ $*_; var $_ int; $*_ }`, 0, `{ type x int }`},
		{`{ $*_; const $_ = $_; $*_ }`, 1, `{ const x = 0 }`},
		{`{ $*_; const $_ = $_; $*_ }`, 0, `{ var x = 0 }`},
		{`if $_ { $*_; var $_ int; $*_ }`, 1, `if cond { var x int }`},
		{`if $_ { $*_; var $_ int; $*_ }`, 0, `if cond { type x int }`},
		{`if $_ { $*_; const $_ = $_; $*_ }`, 1, `if cond { const x = 0 }`},
		{`if $_ { $*_; const $_ = $_; $*_ }`, 0, `if cond { var x = 0 }`},
		{`if $_ { $*_; var $_ int; $*_ }`, 0, `{ var x int }`},
		{`if $_ { $*_; var $_ int; $*_ }`, 0, `{ type x int }`},
		{`if $_ { $*_; const $_ = $_; $*_ }`, 0, `{ const x = 0 }`},
		{`if $_ { $*_; const $_ = $_; $*_ }`, 0, `{ var x = 0 }`},

		// Value specs.
		{`$_ int`, 1, `var a int`},
		{`$_ int`, 0, `var a bool`},
		{`$_ int = 5`, 1, `var x int = 5`},
		{`$_ int = 5`, 0, `var x int`},
		{`$_ int = 5`, 0, `var x int = 0`},
		{`$_ int = 5`, 0, `var x _ = 5`},
		{`$_, $_ int = 10, 20`, 1, `var x, y int = 10, 20`},
		{`$_, $_ int = 10, 20`, 0, `var x, y int = f()`},
		{`$_, $_ int = 10, 20`, 0, `var x, y int = _, 20`},
		{`$_, $_ int = 10, 20`, 0, `var x, y int = 10, _`},
		{`$_, $_ int = 10, 20`, 0, `var x int = 10`},
		{`$_, $_ int = 10, 20`, 0, `var x, y = 10, 20`},
		// TODO: consider these.
		{`$_ int`, 0, `var a int = 3`},
		{`$_ int`, 0, `var a, b int`},
		{`$_ int`, 0, `func(i int) { println(i) }`},

		// File.
		{`package foo`, 1, `package foo;`},
		{`package foo`, 0, `package bar;`},

		// Imports.
		{`import $_`, 1, `package foo; import "fmt"`},
		{`import $_`, 1, `package foo; import ("fmt")`},
		{`import $a`, 1, `package foo; import "fmt"`},
		{`import $a`, 1, `package foo; import ("fmt")`},
	}

	if typeparams.Enabled() {
		tests = append(tests, []testCase{
			// interface{} == any in all contexts.
			// Unless strict mode is
			{`[]any{}`, 1, `[]interface{}{}`},
			{`[]interface{}{}`, 1, `[]any{}`},
			{strict(`[]any{}`), 0, `[]interface{}{}`},
			{strict(`[]any{}`), 1, `[]any{}`},
			{strict(`[]interface{}{}`), 0, `[]any{}`},
			{strict(`[]interface{}{}`), 1, `[]interface{}{}`},
			{`var $_ any`, 1, `var x interface{}`},
			{`var $_ any`, 1, `var x any`},
			{`var $_ any`, 0, `var x int`},

			// Generic func decl.
			{`func f() {}`, 0, `package p; func f[T any]() {}`},
			{`func $_() {}`, 1, `package p; func f[T any]() {}`},
			{`func $_[$_ $_]() {}`, 1, `package p; func f[T any]() {}`},
			{`func $_[$_, $_ $_]() {}`, 1, `package p; func f[T1, T2 any]() {}`},
			{`func $_[$_ $_]() {}`, 0, `package p; func f[T1, T2 any]() {}`},
			{`func $_[$_ any]() {}`, 1, `package p; func f[T1 interface{}]() {}`},
			// TODO: $*_ for type params in declarations
			// {`func $_[$*_]($*_) { $*_ }`, 1, `package p func f[T any]() {}`},
			// {`func $_[$*_]($*_) { $*_ }`, 0, `package p func f() {}`},
			// {`func $_[$*_]() {}`, 1, `package p; func f[T any]() {}`},
			// {`func $_[$*_]() {}`, 1, `package p; func f[T any, T2 any]() {}`},

			// Generic type decl.
			{`type Foo[T any] struct { x T }`, 1, `package p; type Foo[T any] struct { x T }`},
			{`type Foo[T any] struct { x T }`, 0, `package p; type Foo struct { x T }`},
			{`type Foo struct { x T }`, 0, `package p; type Foo[T any] struct { x T }`},

			// Generic literals.
			{`Foo{1}`, 0, `Foo[int]{1}`},
			{`Foo[int]{1}`, 0, `Foo{1}`},
			{`$_{1}`, 1, `Foo[int]{1}`},
			{`$_[$t]{X: 1}`, 1, `Foo[int]{X: 1}`},
			{`$_[$t]{X: 1}`, 0, `Foo{X: 1}`},
			{`$_[$t]{X: 1}`, 0, `Foo[int, int]{X: 1}`},
			// TODO: $*_ for index expressions in generic contexts?

			// Generic calls.
			{`f(10)`, 0, `f[int](10)`},
			{`f[int](10)`, 0, `f(10)`},
			{`$_($*_)`, 1, `f[int](10)`},
			{`$_($*_)`, 1, `f[int](10, 20)`},
			{`$_($*_)`, 1, `f[int, float](10, 20)`},
			{`$_[$t]($*_)`, 1, `f[int](10)`},
			{`$_[$t]($*_)`, 0, `f(10)`},
			{`$_[$t]($*_)`, 0, `f(10, 20)`},
			{`$_[$t, int]($*_)`, 0, `f(10, 20)`},
			{`$_[$t, int]($*_)`, 1, `f[int, int](10, 20)`},
			{`$_[$t, $t]($*_)`, 1, `f[int, int](10, 20)`},
			{`$_[$t, $t]($*_)`, 0, `f[int, uint](10, 20)`},
		}...)
	}

	for i := range tests {
		test := tests[i]
		t.Run(fmt.Sprintf("test%d", i), func(t *testing.T) {
			state := NewMatcherState()
			fset := token.NewFileSet()
			testPattern := unwrapPattern(test.pat)
			config := CompileConfig{
				Fset:   fset,
				Src:    testPattern,
				Strict: isStrict(test.pat),
			}
			pat, _, err := Compile(config)
			if err != nil {
				t.Errorf("compile `%s`: %v", test.pat, err)
				return
			}
			target := testParseNode(t, token.NewFileSet(), test.input)
			matches := 0
			testAllMatches(pat, &state, target, func(m MatchData) {
				matches++
			})
			if matches != test.numMatches {
				t.Errorf("test `%s`:\ntarget: `%s`\nhave: %v\nwant: %v",
					test.pat, test.input, matches, test.numMatches)
			}
		})
	}
}

func testAllMatches(p *Pattern, state *MatcherState, target ast.Node, cb func(MatchData)) {
	visit := func(n ast.Node) bool {
		if n == nil {
			return false
		}
		p.MatchNode(state, n, cb)
		return true
	}
	ast.Inspect(target, visit)
}
