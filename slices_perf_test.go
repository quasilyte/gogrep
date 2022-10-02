package gogrep

import (
	"go/ast"
	"testing"
)

func BenchmarkExprSlice(b *testing.B) {
	slice := &NodeSlice{
		Kind: ExprNodeSlice,
		exprSlice: []ast.Expr{
			&ast.Ident{Name: "a"},
			&ast.Ident{Name: "b"},
			&ast.Ident{Name: "c"},
			&ast.Ident{Name: "d"},
		},
	}

	b.Run("get", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			l := slice.Len()
			for j := 0; j < l; j++ {
				n := slice.At(j)
				if n == nil {
					b.Fail()
				}
			}
		}
	})

	b.Run("slice", func(b *testing.B) {
		var dst NodeSlice
		for i := 0; i < b.N; i++ {
			slice.SliceInto(&dst, 0, 2)
		}
		if dst.Len() == 0 {
			b.Fail()
		}
	})

	b.Run("pos", func(b *testing.B) {
		total := 0
		for i := 0; i < b.N; i++ {
			total += int(slice.Pos())
			total += int(slice.End())
		}
		if total == 0 {
			b.Fail()
		}
	})
}
