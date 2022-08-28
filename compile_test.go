package gogrep

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/exp/typeparams"
)

type compileTest struct {
	input  string
	output []string
}

func runCompileTest(t *testing.T, i int, test compileTest, withTypes bool) {
	t.Helper()
	t.Run(fmt.Sprintf("test%d", i), func(t *testing.T) {
		isStrict := func(s string) bool {
			return strings.HasPrefix(s, "STRICT ")
		}
		unwrapPattern := func(s string) string {
			s = strings.TrimPrefix(s, "STRICT ")
			return s
		}

		customImports := map[string]string{
			"errors": "github.com/pkg/errors",
		}

		strict := isStrict(test.input)
		input := unwrapPattern(test.input)
		want := test.output
		fset := token.NewFileSet()
		config := CompileConfig{
			Fset:      fset,
			Src:       input,
			Strict:    strict,
			WithTypes: withTypes,
			Imports:   customImports,
		}
		p, _, err := Compile(config)
		if err != nil {
			t.Errorf("compile `%s`: %v", input, err)
			return
		}
		have := formatProgram(p.m.prog)
		if diff := cmp.Diff(have, want); diff != "" {
			t.Errorf("compile `%s` (+want -have):\n%s", input, diff)
			fmt.Printf("Output:\n")
			for _, line := range have {
				fmt.Printf("`%s`,\n", line)
			}
			return
		}
	})
}

func compileTestsFromMap(m map[string][]string) []compileTest {
	result := make([]compileTest, 0, len(m))
	for input, output := range m {
		result = append(result, compileTest{input: input, output: output})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].input < result[j].input
	})
	return result
}

func TestCompileWildcard(t *testing.T) {
	tests := compileTestsFromMap(map[string][]string{
		`$_`:  {`Node`},
		`$x`:  {`NamedNode x`},
		`$*_`: {`NodeSeq`},
		`$*x`: {`NamedNodeSeq x`},

		`a.$x`: {
			`SelectorExpr`,
			` • NamedNode x`,
			` • Ident a`,
		},
		`$x.b`: {
			`SimpleSelectorExpr b`,
			` • NamedNode x`,
		},

		`if $x != (nil) { $y }`: {
			`IfStmt`,
			` • BinaryExpr !=`,
			` •  • NamedNode x`,
			` •  • ParenExpr`,
			` •  •  • Ident nil`,
			` • BlockStmt`,
			` •  • NamedNode y`,
			` •  • End`,
		},
		`if $*_ {}`: {
			`IfInitStmt`,
			` • OptNode`,
			` • Node`,
			` • BlockStmt`,
			` •  • End`,
		},
		`if $*_ {} else {}`: {
			`IfInitElseStmt`,
			` • OptNode`,
			` • Node`,
			` • BlockStmt`,
			` •  • End`,
			` • BlockStmt`,
			` •  • End`,
		},
		`if $*x {} else {}`: {
			`IfNamedOptElseStmt x`,
			` • BlockStmt`,
			` •  • End`,
			` • BlockStmt`,
			` •  • End`,
		},
		`if $*x {} else if $*x {}`: {
			`IfNamedOptElseStmt x`,
			` • BlockStmt`,
			` •  • End`,
			` • IfNamedOptStmt x`,
			` •  • BlockStmt`,
			` •  •  • End`,
		},
		`if $*x {}`: {
			`IfNamedOptStmt x`,
			` • BlockStmt`,
			` •  • End`,
		},
		`if $_; cond {}`: {
			`IfInitStmt`,
			` • Node`,
			` • Ident cond`,
			` • BlockStmt`,
			` •  • End`,
		},
		`if $*_; cond {}`: {
			`IfInitStmt`,
			` • OptNode`,
			` • Ident cond`,
			` • BlockStmt`,
			` •  • End`,
		},
		`if $*x; cond {}`: {
			`IfInitStmt`,
			` • NamedOptNode x`,
			` • Ident cond`,
			` • BlockStmt`,
			` •  • End`,
		},

		`func ($x typ) {}`: {
			`FuncLit`,
			` • VoidFuncType`,
			` •  • FieldList`,
			` •  •  • Field`,
			` •  •  •  • NamedNode x`,
			` •  •  •  • Ident typ`,
			` •  •  • End`,
			` • BlockStmt`,
			` •  • End`,
		},

		`print($*_, x, $*_)`: {
			`CallExpr`,
			` • Ident print`,
			` • ArgList`,
			` •  • NodeSeq`,
			` •  • Ident x`,
			` •  • NodeSeq`,
			` •  • End`,
		},

		`{ $*_; x; $*_ }`: {
			`BlockStmt`,
			` • NodeSeq`,
			` • ExprStmt`,
			` •  • Ident x`,
			` • NodeSeq`,
			` • End`,
		},
		`{ $*head; x }`: {
			`BlockStmt`,
			` • NamedNodeSeq head`,
			` • ExprStmt`,
			` •  • Ident x`,
			` • End`,
		},

		`$l: if c {}`: {
			`LabeledStmt`,
			` • NamedNode l`,
			` • IfStmt`,
			` •  • Ident c`,
			` •  • BlockStmt`,
			` •  •  • End`,
		},

		`goto $l`: {
			`LabeledBranchStmt goto`,
			` • NamedNode l`,
		},

		`for $*_; $*_; $*_ {}`: {
			`ForInitCondPostStmt`,
			` • OptNode`,
			` • OptNode`,
			` • OptNode`,
			` • BlockStmt`,
			` •  • End`,
		},

		`const $x = $y`: {
			`ConstDecl`,
			` • ValueInitSpec`,
			` •  • NamedNode x`,
			` •  • End`,
			` •  • NamedNode y`,
			` •  • End`,
			` • End`,
		},

		`const ($_ $_ = iota; $_; $*_)`: {
			`ConstDecl`,
			` • TypedValueInitSpec`,
			` •  • Node`,
			` •  • End`,
			` •  • Node`,
			` •  • Ident iota`,
			` •  • End`,
			` • Node`,
			` • NodeSeq`,
			` • End`,
		},

		`$_ int`: {
			`TypedValueSpec`,
			` • Node`,
			` • End`,
			` • Ident int`,
		},
		`$_ int = 5`: {
			`TypedValueInitSpec`,
			` • Node`,
			` • End`,
			` • Ident int`,
			` • BasicLit 5`,
			` • End`,
		},

		`switch {$_}`: {
			`SwitchStmt`,
			` • Node`,
			` • End`,
		},

		`switch $*_; x.(type) {}`: {
			`TypeSwitchInitStmt`,
			` • OptNode`,
			` • ExprStmt`,
			` •  • TypeSwitchAssertExpr`,
			` •  •  • Ident x`,
			` • End`,
		},

		`select {$*x}`: {
			`SelectStmt`,
			` • NamedNodeSeq x`,
			` • End`,
		},

		`package $p`: {
			`EmptyPackage`,
			` • NamedNode p`,
		},

		// $*_ in a place of a field list implies a field list of 0 or more fields.
		// It can also match a field list of 1 element and nil.
		`func $_() $*_ { $*_ }`: {
			`FuncDecl`,
			` • Node`,
			` • FuncType`,
			` •  • FieldList`,
			` •  •  • End`,
			` •  • OptNode`,
			` • BlockStmt`,
			` •  • NodeSeq`,
			` •  • End`,
		},

		// $y in a place of a field list implies a field list of exactly 1 field.
		`func $_($x $y) $y { return $x }`: {
			`FuncDecl`,
			` • Node`,
			` • FuncType`,
			` •  • FieldList`,
			` •  •  • Field`,
			` •  •  •  • NamedNode x`,
			` •  •  •  • NamedNode y`,
			` •  •  • End`,
			` •  • NamedFieldNode y`,
			` • BlockStmt`,
			` •  • ReturnStmt`,
			` •  •  • NamedNode x`,
			` •  •  • End`,
			` •  • End`,
		},

		`func _($*_) {}`: {
			`SimpleFuncDecl _`,
			` • VoidFuncType`,
			` •  • OptNode`,
			` • BlockStmt`,
			` •  • End`,
		},

		`f($*_)`: {
			`CallExpr`,
			` • Ident f`,
			` • ArgList`,
			` •  • NodeSeq`,
			` •  • End`,
		},

		`f(1, $*_)`: {
			`MaybeVariadicCallExpr 1`,
			` • Ident f`,
			` • ArgList`,
			` •  • BasicLit 1`,
			` •  • NodeSeq`,
			` •  • End`,
		},

		`f(1, 2, 3, $*_)`: {
			`MaybeVariadicCallExpr 3`,
			` • Ident f`,
			` • ArgList`,
			` •  • BasicLit 1`,
			` •  • BasicLit 2`,
			` •  • BasicLit 3`,
			` •  • NodeSeq`,
			` •  • End`,
		},

		`f($_)`: {
			`NonVariadicCallExpr`,
			` • Ident f`,
			` • SimpleArgList 1`,
			` •  • Node`,
		},

		`for { var $_ = $_ }`: {
			`ForStmt`,
			` • BlockStmt`,
			` •  • DeclStmt`,
			` •  •  • VarDecl`,
			` •  •  •  • ValueInitSpec`,
			` •  •  •  •  • Node`,
			` •  •  •  •  • End`,
			` •  •  •  •  • Node`,
			` •  •  •  •  • End`,
			` •  •  •  • End`,
			` •  • End`,
		},

		`var x int; if true { f() }`: {
			`MultiStmt`,
			` • DeclStmt`,
			` •  • VarDecl`,
			` •  •  • TypedValueSpec`,
			` •  •  •  • Ident x`,
			` •  •  •  • End`,
			` •  •  •  • Ident int`,
			` •  •  • End`,
			` • IfStmt`,
			` •  • Ident true`,
			` •  • BlockStmt`,
			` •  •  • ExprStmt`,
			` •  •  •  • NonVariadicCallExpr`,
			` •  •  •  •  • Ident f`,
			` •  •  •  •  • SimpleArgList 0`,
			` •  •  • End`,
			` • End`,
		},

		`struct{$*_; Foo; $*_}`: {
			`StructType`,
			` • FieldList`,
			` •  • NodeSeq`,
			` •  • UnnamedField`,
			` •  •  • Ident Foo`,
			` •  • NodeSeq`,
			` •  • End`,
		},

		`func $_($*_) $_ { $*_ }`: {
			`FuncDecl`,
			` • Node`,
			` • FuncType`,
			` •  • OptNode`,
			` •  • FieldNode`,
			` • BlockStmt`,
			` •  • NodeSeq`,
			` •  • End`,
		},

		`s[$*_:$*_]`: {
			`SliceFromToExpr`,
			` • Ident s`,
			` • OptNode`,
			` • OptNode`,
		},

		`s[$*_:]`: {
			`SliceFromExpr`,
			` • Ident s`,
			` • OptNode`,
		},

		`const $_ $*_ = iota`: {
			`ConstDecl`,
			` • TypedValueInitSpec`,
			` •  • Node`,
			` •  • End`,
			` •  • OptNode`,
			` •  • Ident iota`,
			` •  • End`,
			` • End`,
		},

		`import $_`: {
			`AnyImportDecl`,
		},

		`import $imports`: {
			`ImportDecl`,
			` • NamedNodeSeq imports`,
			` • End`,
		},

		`importFoo()`: {
			`NonVariadicCallExpr`,
			` • Ident importFoo`,
			` • SimpleArgList 0`,
		},

		`range ($_)`: {
			`RangeClause`,
			` • ParenExpr`,
			` •  • Node`,
		},

		`for $_, $v := range $xs`: {
			`RangeKeyValueHeader :=`,
			` • Node`,
			` • NamedNode v`,
			` • NamedNode xs`,
		},

		`f($_, $*_)`: {
			`MaybeVariadicCallExpr 1`,
			` • Ident f`,
			` • ArgList`,
			` •  • Node`,
			` •  • NodeSeq`,
			` •  • End`,
		},
	})

	if typeparams.Enabled() {
		tests = append(tests, compileTestsFromMap(map[string][]string{
			`func $_[$_ $_]() {}`: {
				`FuncDecl`,
				` • Node`,
				` • GenericVoidFuncType`,
				` •  • FieldList`,
				` •  •  • Field`,
				` •  •  •  • Node`,
				` •  •  •  • Node`,
				` •  •  • End`,
				` •  • FieldList`,
				` •  •  • End`,
				` • BlockStmt`,
				` •  • End`,
			},
		})...)
	}

	for i := range tests {
		runCompileTest(t, i, tests[i], false)
	}
}

func TestCompileWildcardWithTypes(t *testing.T) {
	tests := compileTestsFromMap(map[string][]string{
		`fmt.$_($*_)`: {
			`CallExpr`,
			` • SelectorExpr`,
			` •  • Node`,
			` •  • Ident fmt`,
			` • ArgList`,
			` •  • NodeSeq`,
			` •  • End`,
		},
	})

	for i := range tests {
		runCompileTest(t, i, tests[i], true)
	}
}

func TestCompileWithTypes(t *testing.T) {
	tests := compileTestsFromMap(map[string][]string{
		`package p;`: {
			`EmptyPackage`,
			` • Ident p`,
		},

		`[]interface{}{}`: {
			`TypedCompositeLit`,
			` • SliceType`,
			` •  • EfaceType`,
			` • End`,
		},

		`var x interface{}`: {
			`VarDecl`,
			` • TypedValueSpec`,
			` •  • Ident x`,
			` •  • End`,
			` •  • EfaceType`,
			` • End`,
		},

		`var ()`: {
			`VarDecl`,
			` • End`,
		},
		`type foo = int`: {
			`TypeDecl`,
			` • TypeAliasSpec`,
			` •  • Ident foo`,
			` •  • Ident int`,
			` • End`,
		},
		`type (a int64; b string)`: {
			`TypeDecl`,
			` • SimpleTypeSpec a`,
			` •  • Ident int64`,
			` • SimpleTypeSpec b`,
			` •  • Ident string`,
			` • End`,
		},

		`10`:    {`BasicLit 10`},
		`2.4`:   {`BasicLit 2.4`},
		`"foo"`: {`BasicLit "foo"`},
		`'a'`:   {`BasicLit 97`},
		`'\n'`:  {`BasicLit 10`},
		`'✓'`:   {`BasicLit 10003`},

		`*x`: {
			`StarExpr`,
			` • Ident x`,
		},
		`+x`: {
			`UnaryExpr +`,
			` • Ident x`,
		},
		`-x`: {
			`UnaryExpr -`,
			` • Ident x`,
		},
		`((x))`: {
			`ParenExpr`,
			` • ParenExpr`,
			` •  • Ident x`,
		},

		`[]func() int{}`: {
			`TypedCompositeLit`,
			` • SliceType`,
			` •  • FuncType`,
			` •  •  • FieldList`,
			` •  •  •  • End`,
			` •  •  • FieldList`,
			` •  •  •  • UnnamedField`,
			` •  •  •  •  • Ident int`,
			` •  •  •  • End`,
			` • End`,
		},

		`func () {}`: {
			`FuncLit`,
			` • VoidFuncType`,
			` •  • FieldList`,
			` •  •  • End`,
			` • BlockStmt`,
			` •  • End`,
		},
		`func(xs ...int) {}`: {
			`FuncLit`,
			` • VoidFuncType`,
			` •  • FieldList`,
			` •  •  • SimpleField xs`,
			` •  •  •  • TypedEllipsis`,
			` •  •  •  •  • Ident int`,
			` •  •  • End`,
			` • BlockStmt`,
			` •  • End`,
		},
		`func(x, y int, z int) (string, string) {}`: {
			`FuncLit`,
			` • FuncType`,
			` •  • FieldList`,
			` •  •  • MultiField`,
			` •  •  •  • Ident x`,
			` •  •  •  • Ident y`,
			` •  •  •  • End`,
			` •  •  •  • Ident int`,
			` •  •  • SimpleField z`,
			` •  •  •  • Ident int`,
			` •  •  • End`,
			` •  • FieldList`,
			` •  •  • UnnamedField`,
			` •  •  •  • Ident string`,
			` •  •  • UnnamedField`,
			` •  •  •  • Ident string`,
			` •  •  • End`,
			` • BlockStmt`,
			` •  • End`,
		},

		`1 + 2`: {
			`BinaryExpr +`,
			` • BasicLit 1`,
			` • BasicLit 2`,
		},
		`1 - (x)`: {
			`BinaryExpr -`,
			` • BasicLit 1`,
			` • ParenExpr`,
			` •  • Ident x`,
		},

		`f(1, 2)`: {
			`NonVariadicCallExpr`,
			` • Ident f`,
			` • SimpleArgList 2`,
			` •  • BasicLit 1`,
			` •  • BasicLit 2`,
		},

		`f(g(), xs...)`: {
			`VariadicCallExpr`,
			` • Ident f`,
			` • SimpleArgList 2`,
			` •  • NonVariadicCallExpr`,
			` •  •  • Ident g`,
			` •  •  • SimpleArgList 0`,
			` •  • Ident xs`,
		},

		`x[0]`: {
			`IndexExpr`,
			` • Ident x`,
			` • BasicLit 0`,
		},

		`x[int, uint]`: {
			`IndexListExpr`,
			` • Ident x`,
			` • Ident int`,
			` • Ident uint`,
			` • End`,
		},

		`s[:]`: {
			`SliceExpr`,
			` • Ident s`,
		},
		`s[from:]`: {
			`SliceFromExpr`,
			` • Ident s`,
			` • Ident from`,
		},
		`s[:to]`: {
			`SliceToExpr`,
			` • Ident s`,
			` • Ident to`,
		},
		`s[from:to]`: {
			`SliceFromToExpr`,
			` • Ident s`,
			` • Ident from`,
			` • Ident to`,
		},
		`s[:to:max]`: {
			`SliceToCapExpr`,
			` • Ident s`,
			` • Ident to`,
			` • Ident max`,
		},
		`s[from:to:max]`: {
			`SliceFromToCapExpr`,
			` • Ident s`,
			` • Ident from`,
			` • Ident to`,
			` • Ident max`,
		},

		`([2]int)(x)`: {
			`NonVariadicCallExpr`,
			` • ParenExpr`,
			` •  • ArrayType`,
			` •  •  • BasicLit 2`,
			` •  •  • Ident int`,
			` • SimpleArgList 1`,
			` •  • Ident x`,
		},
		`([]int)(x)`: {
			`NonVariadicCallExpr`,
			` • ParenExpr`,
			` •  • SliceType`,
			` •  •  • Ident int`,
			` • SimpleArgList 1`,
			` •  • Ident x`,
		},

		`[]int{1, 2}`: {
			`TypedCompositeLit`,
			` • SliceType`,
			` •  • Ident int`,
			` • BasicLit 1`,
			` • BasicLit 2`,
			` • End`,
		},
		`[][]int{{1, 2}, {3}}`: {
			`TypedCompositeLit`,
			` • SliceType`,
			` •  • SliceType`,
			` •  •  • Ident int`,
			` • CompositeLit`,
			` •  • BasicLit 1`,
			` •  • BasicLit 2`,
			` •  • End`,
			` • CompositeLit`,
			` •  • BasicLit 3`,
			` •  • End`,
			` • End`,
		},

		`[...]int{5: 1}`: {
			`TypedCompositeLit`,
			` • ArrayType`,
			` •  • Ellipsis`,
			` •  • Ident int`,
			` • KeyValueExpr`,
			` •  • BasicLit 5`,
			` •  • BasicLit 1`,
			` • End`,
		},
		`map[int]string{}`: {
			`TypedCompositeLit`,
			` • MapType`,
			` •  • Ident int`,
			` •  • Ident string`,
			` • End`,
		},

		`go f()`: {
			`GoStmt`,
			` • NonVariadicCallExpr`,
			` •  • Ident f`,
			` •  • SimpleArgList 0`,
		},

		`defer f()`: {
			`DeferStmt`,
			` • NonVariadicCallExpr`,
			` •  • Ident f`,
			` •  • SimpleArgList 0`,
		},

		`ch <- 1`: {
			`SendStmt`,
			` • Ident ch`,
			` • BasicLit 1`,
		},

		`x.y.z`: {
			`SimpleSelectorExpr z`,
			` • SimpleSelectorExpr y`,
			` •  • Ident x`,
		},

		`x.(int)`: {
			`TypeAssertExpr`,
			` • Ident x`,
			` • Ident int`,
		},

		`;`: {`EmptyStmt`},

		`x++`: {
			`IncDecStmt ++`,
			` • Ident x`,
		},
		`x--`: {
			`IncDecStmt --`,
			` • Ident x`,
		},

		`{ f(); g(); }`: {
			`BlockStmt`,
			` • ExprStmt`,
			` •  • NonVariadicCallExpr`,
			` •  •  • Ident f`,
			` •  •  • SimpleArgList 0`,
			` • ExprStmt`,
			` •  • NonVariadicCallExpr`,
			` •  •  • Ident g`,
			` •  •  • SimpleArgList 0`,
			` • End`,
		},

		`if cond {}`: {
			`IfStmt`,
			` • Ident cond`,
			` • BlockStmt`,
			` •  • End`,
		},
		`if init; cond {}`: {
			`IfInitStmt`,
			` • ExprStmt`,
			` •  • Ident init`,
			` • Ident cond`,
			` • BlockStmt`,
			` •  • End`,
		},
		`if cond {} else { f() }`: {
			`IfElseStmt`,
			` • Ident cond`,
			` • BlockStmt`,
			` •  • End`,
			` • BlockStmt`,
			` •  • ExprStmt`,
			` •  •  • NonVariadicCallExpr`,
			` •  •  •  • Ident f`,
			` •  •  •  • SimpleArgList 0`,
			` •  • End`,
		},
		`if cond {} else if cond2 { f() } else {}`: {
			`IfElseStmt`,
			` • Ident cond`,
			` • BlockStmt`,
			` •  • End`,
			` • IfElseStmt`,
			` •  • Ident cond2`,
			` •  • BlockStmt`,
			` •  •  • ExprStmt`,
			` •  •  •  • NonVariadicCallExpr`,
			` •  •  •  •  • Ident f`,
			` •  •  •  •  • SimpleArgList 0`,
			` •  •  • End`,
			` •  • BlockStmt`,
			` •  •  • End`,
		},
		`if init1; cond {} else if init2; cond2 { f() } else {}`: {
			`IfInitElseStmt`,
			` • ExprStmt`,
			` •  • Ident init1`,
			` • Ident cond`,
			` • BlockStmt`,
			` •  • End`,
			` • IfInitElseStmt`,
			` •  • ExprStmt`,
			` •  •  • Ident init2`,
			` •  • Ident cond2`,
			` •  • BlockStmt`,
			` •  •  • ExprStmt`,
			` •  •  •  • NonVariadicCallExpr`,
			` •  •  •  •  • Ident f`,
			` •  •  •  •  • SimpleArgList 0`,
			` •  •  • End`,
			` •  • BlockStmt`,
			` •  •  • End`,
		},

		`return 1, 2`: {
			`ReturnStmt`,
			` • BasicLit 1`,
			` • BasicLit 2`,
			` • End`,
		},

		`break`:       {`BranchStmt break`},
		`continue`:    {`BranchStmt continue`},
		`fallthrough`: {`BranchStmt fallthrough`},
		`break l`:     {`SimpleLabeledBranchStmt break l`},
		`continue l`:  {`SimpleLabeledBranchStmt continue l`},
		`goto l`:      {`SimpleLabeledBranchStmt goto l`},

		`foo: x`: {
			`KeyValueExpr`,
			` • Ident foo`,
			` • Ident x`,
		},

		`{foo: x}`: {
			`BlockStmt`,
			` • SimpleLabeledStmt foo`,
			` •  • ExprStmt`,
			` •  •  • Ident x`,
			` • End`,
		},

		`x = y`: {
			`AssignStmt =`,
			` • Ident x`,
			` • Ident y`,
		},
		`x := y`: {
			`AssignStmt :=`,
			` • Ident x`,
			` • Ident y`,
		},
		`x, y := f()`: {
			`MultiAssignStmt :=`,
			` • Ident x`,
			` • Ident y`,
			` • End`,
			` • NonVariadicCallExpr`,
			` •  • Ident f`,
			` •  • SimpleArgList 0`,
			` • End`,
		},

		`(chan int)(nil)`: {
			`NonVariadicCallExpr`,
			` • ParenExpr`,
			` •  • ChanType send recv`,
			` •  •  • Ident int`,
			` • SimpleArgList 1`,
			` •  • Ident nil`,
		},
		`(chan<- int)(nil)`: {
			`NonVariadicCallExpr`,
			` • ParenExpr`,
			` •  • ChanType send`,
			` •  •  • Ident int`,
			` • SimpleArgList 1`,
			` •  • Ident nil`,
		},
		`(<-chan int)(nil)`: {
			`NonVariadicCallExpr`,
			` • ParenExpr`,
			` •  • ChanType recv`,
			` •  •  • Ident int`,
			` • SimpleArgList 1`,
			` •  • Ident nil`,
		},

		`for range xs {}`: {
			`RangeStmt`,
			` • Ident xs`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for i := range xs {}`: {
			`RangeKeyStmt :=`,
			` • Ident i`,
			` • Ident xs`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for i = range xs {}`: {
			`RangeKeyStmt =`,
			` • Ident i`,
			` • Ident xs`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for i, x := range xs {}`: {
			`RangeKeyValueStmt :=`,
			` • Ident i`,
			` • Ident x`,
			` • Ident xs`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for i, x = range xs {}`: {
			`RangeKeyValueStmt =`,
			` • Ident i`,
			` • Ident x`,
			` • Ident xs`,
			` • BlockStmt`,
			` •  • End`,
		},

		`for {}`: {
			`ForStmt`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for ;; {}`: {
			`ForStmt`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for ;; post {}`: {
			`ForPostStmt`,
			` • ExprStmt`,
			` •  • Ident post`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for cond {}`: {
			`ForCondStmt`,
			` • Ident cond`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for ; cond; {}`: {
			`ForCondStmt`,
			` • Ident cond`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for ; cond; post {}`: {
			`ForCondPostStmt`,
			` • Ident cond`,
			` • ExprStmt`,
			` •  • Ident post`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for init; ; {}`: {
			`ForInitStmt`,
			` • ExprStmt`,
			` •  • Ident init`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for init; ; post {}`: {
			`ForInitPostStmt`,
			` • ExprStmt`,
			` •  • Ident init`,
			` • ExprStmt`,
			` •  • Ident post`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for init; cond; {}`: {
			`ForInitCondStmt`,
			` • ExprStmt`,
			` •  • Ident init`,
			` • Ident cond`,
			` • BlockStmt`,
			` •  • End`,
		},
		`for init; cond; post {}`: {
			`ForInitCondPostStmt`,
			` • ExprStmt`,
			` •  • Ident init`,
			` • Ident cond`,
			` • ExprStmt`,
			` •  • Ident post`,
			` • BlockStmt`,
			` •  • End`,
		},

		`switch x.(type) {}`: {
			`TypeSwitchStmt`,
			` • ExprStmt`,
			` •  • TypeSwitchAssertExpr`,
			` •  •  • Ident x`,
			` • End`,
		},

		`switch x := y.(type) {}`: {
			`TypeSwitchStmt`,
			` • AssignStmt :=`,
			` •  • Ident x`,
			` •  • TypeSwitchAssertExpr`,
			` •  •  • Ident y`,
			` • End`,
		},

		`switch {case 1, 2: f(); default: g() }`: {
			`SwitchStmt`,
			` • CaseClause`,
			` •  • BasicLit 1`,
			` •  • BasicLit 2`,
			` •  • End`,
			` •  • ExprStmt`,
			` •  •  • NonVariadicCallExpr`,
			` •  •  •  • Ident f`,
			` •  •  •  • SimpleArgList 0`,
			` •  • End`,
			` • DefaultCaseClause`,
			` •  • ExprStmt`,
			` •  •  • NonVariadicCallExpr`,
			` •  •  •  • Ident g`,
			` •  •  •  • SimpleArgList 0`,
			` •  • End`,
			` • End`,
		},

		`fmt.Println(5, 6)`: {
			`NonVariadicCallExpr`,
			` • SimpleSelectorExpr Println`,
			` •  • Pkg fmt`,
			` • SimpleArgList 2`,
			` •  • BasicLit 5`,
			` •  • BasicLit 6`,
		},

		`x = fmt.Sprint(y)`: {
			`AssignStmt =`,
			` • Ident x`,
			` • NonVariadicCallExpr`,
			` •  • SimpleSelectorExpr Sprint`,
			` •  •  • Pkg fmt`,
			` •  • SimpleArgList 1`,
			` •  •  • Ident y`,
		},

		`rand.Intn(10)`: {
			`NonVariadicCallExpr`,
			` • SimpleSelectorExpr Intn`,
			` •  • Pkg math/rand`,
			` • SimpleArgList 1`,
			` •  • BasicLit 10`,
		},

		`return errors.New("foo")`: {
			`ReturnStmt`,
			` • NonVariadicCallExpr`,
			` •  • SimpleSelectorExpr New`,
			` •  •  • Pkg github.com/pkg/errors`,
			` •  • SimpleArgList 1`,
			` •  •  • BasicLit "foo"`,
			` • End`,
		},

		`const (x = 1; y = 2)`: {
			`ConstDecl`,
			` • ValueInitSpec`,
			` •  • Ident x`,
			` •  • End`,
			` •  • BasicLit 1`,
			` •  • End`,
			` • ValueInitSpec`,
			` •  • Ident y`,
			` •  • End`,
			` •  • BasicLit 2`,
			` •  • End`,
			` • End`,
		},

		`const (x = iota; y)`: {
			`ConstDecl`,
			` • ValueInitSpec`,
			` •  • Ident x`,
			` •  • End`,
			` •  • Ident iota`,
			` •  • End`,
			` • ValueSpec`,
			` •  • Ident y`,
			` • End`,
		},
	})

	if typeparams.Enabled() {
		tests = append(tests, compileTestsFromMap(map[string][]string{
			`var x any`: {
				`VarDecl`,
				` • TypedValueSpec`,
				` •  • Ident x`,
				` •  • End`,
				` •  • EfaceType`,
				` • End`,
			},

			`[]any{}`: {
				`TypedCompositeLit`,
				` • SliceType`,
				` •  • EfaceType`,
				` • End`,
			},

			`type Foo[T any] struct{}`: {
				`TypeDecl`,
				` • GenericTypeSpec`,
				` •  • Ident Foo`,
				` •  • FieldList`,
				` •  •  • SimpleField T`,
				` •  •  •  • EfaceType`,
				` •  •  • End`,
				` •  • StructType`,
				` •  •  • FieldList`,
				` •  •  •  • End`,
				` • End`,
			},
		})...)
	}

	for i := range tests {
		runCompileTest(t, i, tests[i], true)
	}
}

func testParseNode(t testing.TB, fset *token.FileSet, s string) ast.Node {
	if strings.HasPrefix(s, "package ") {
		file, err := parser.ParseFile(fset, "string", s, 0)
		if err != nil {
			t.Fatalf("parse `%s`: %v", s, err)
		}
		return file
	}
	source := `package p; func _() { ` + s + ` }`
	file, err := parser.ParseFile(fset, "string", source, 0)
	if err != nil {
		t.Fatalf("parse `%s`: %v", s, err)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	n := fn.Body.List[0]
	if e, ok := n.(*ast.ExprStmt); ok {
		return e.X
	}
	return n
}
