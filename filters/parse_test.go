package filters

import (
	"fmt"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input string
		expr  string
		info  string
	}{
		{
			input: `file.IsTest()`,
			expr:  `Nop`,
			info:  `file.IsTest()`,
		},
		{
			input: `!file.IsTest()`,
			expr:  `Nop`,
			info:  `!file.IsTest()`,
		},
		{
			input: `!(!file.IsTest())`,
			expr:  `Nop`,
			info:  `file.IsTest()`,
		},
		{
			input: `file.IsAutogen()`,
			expr:  `Nop`,
			info:  `file.IsAutogen()`,
		},

		{
			input: `function.IsHot()`,
			expr:  `(FunctionVarFunc "IsHot")`,
			info:  `function.IsHot()`,
		},
		{
			input: `!function.IsHot()`,
			expr:  `(Not (FunctionVarFunc "IsHot"))`,
			info:  `!function.IsHot()`,
		},
		{
			input: `function.IsHot() && file.IsMain()`,
			expr:  `(FunctionVarFunc "IsHot")`,
			info:  `file.IsMain() function.IsHot()`,
		},
		{
			input: `file.IsMain() && function.IsHot()`,
			expr:  `(FunctionVarFunc "IsHot")`,
			info:  `file.IsMain() function.IsHot()`,
		},
		{
			input: `function.IsHot() && $x.IsPure()`,
			expr:  `(And (FunctionVarFunc "IsHot") (%IsPure "x"))`,
			info:  `function.IsHot() $x`,
		},
		{
			input: `$x.IsPure() && function.IsHot()`,
			expr:  `(And (%IsPure "x") (FunctionVarFunc "IsHot"))`,
			info:  `function.IsHot() $x`,
		},
		{
			input: `$x.IsPure() || function.IsHot()`,
			expr:  `(Or (%IsPure "x") (FunctionVarFunc "IsHot"))`,
			info:  `$x`,
		},
		{
			input: `!($x.IsPure() || function.IsHot())`,
			expr:  `(Not (Or (%IsPure "x") (FunctionVarFunc "IsHot")))`,
			info:  `$x`,
		},

		{
			input: `$x.IsPure()`,
			expr:  `(%IsPure "x")`,
			info:  `$x`,
		},
		{
			input: `$x.IsPure() || $y.IsPure()`,
			expr:  `(Or (%IsPure "x") (%IsPure "y"))`,
			info:  `$x $y`,
		},

		{
			input: `!file.IsAutogen() && (!$x.IsPure() || !$y.IsPure())`,
			expr:  `(Or (Not (%IsPure "x")) (Not (%IsPure "y")))`,
			info:  `!file.IsAutogen() $x $y`,
		},

		{
			input: `(file.IsAutogen()) && !file.IsTest()`,
			expr:  `Nop`,
			info:  `file.IsAutogen() !file.IsTest()`,
		},

		{
			input: `file.IsTest() && $c.IsConst()`,
			expr:  `(%IsConst "c")`,
			info:  `file.IsTest() $c`,
		},
		{
			input: `$c.IsConst() && file.IsTest()`,
			expr:  `(%IsConst "c")`,
			info:  `file.IsTest() $c`,
		},
		{
			input: `$x.IsConst() && !($y.IsConst() && file.IsTest())`,
			expr:  `(And (%IsConst "x") (Not (%IsConst "y")))`,
			info:  `!file.IsTest() $x $y`,
		},

		{
			input: `$$.IsPure()`,
			expr:  `(%IsPure "_Dollar2_")`,
			info:  `$_Dollar2_`,
		},

		{
			input: `$x.Text() == "String"`,
			expr:  `(Eq (%Text "x") (String "String"))`,
			info:  `$x`,
		},
		{
			input: `"String" == $x.Text()`,
			expr:  `(Eq (%Text "x") (String "String"))`,
			info:  `$x`,
		},
		{
			input: `$x.Text() != "String"`,
			expr:  `(NotEq (%Text "x") (String "String"))`,
			info:  `$x`,
		},
		{
			input: `"String" != $x.Text()`,
			expr:  `(NotEq (%Text "x") (String "String"))`,
			info:  `$x`,
		},
	}

	const (
		opVarIsConst = iota + 1
		opVarIsPure
		opVarText
	)
	varOps := map[string]Operation{
		"IsConst": opVarIsConst,
		"IsPure":  opVarIsPure,
		"Text":    opVarText,
	}
	optab := NewOperationTable(varOps)

	for i := range tests {
		test := tests[i]
		t.Run(fmt.Sprintf("test%d", i), func(t *testing.T) {
			compiled, info, err := Parse(optab, test.input)
			if err != nil {
				t.Fatalf("compile %q: %v", test.input, err)
			}
			if test.info != info.String() {
				t.Fatalf("info mismatch for %q:\nhave: %s\nwant: %s",
					test.input, info.String(), test.info)
			}
			have := Sprint(&info, compiled)
			if test.expr != have {
				t.Fatalf("result mismatch for %q:\nhave: %s\nwant: %s", test.input, have, test.expr)
			}
		})
	}
}
