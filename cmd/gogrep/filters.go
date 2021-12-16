package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"

	"github.com/quasilyte/gogrep"
	"github.com/quasilyte/gogrep/filters"
)

type bool3 byte

const (
	bool3unset bool3 = iota
	bool3false
	bool3true
)

func newBool3(v bool) bool3 {
	if v {
		return bool3true
	}
	return bool3false
}

func (b bool3) Eq(v bool) bool {
	if v {
		return b == bool3true
	}
	return b == bool3false
}

type filterHints struct {
	autogenCond bool3
}

const (
	opVarIsPure filters.Operation = iota + 1
	opVarIsConst
	opVarIsStringLit
	opVarIsRuneLit
	opVarIsIntLit
	opVarIsFloatLit
	opVarIsComplexLit
)

func applyFilter(f *filters.Expr, n ast.Node, m gogrep.MatchData) bool {
	switch f.Op {
	case filters.OpNot:
		return !applyFilter(f.Args[0], n, m)

	case filters.OpAnd:
		return applyFilter(f.Args[0], n, m) && applyFilter(f.Args[1], n, m)

	case filters.OpOr:
		return applyFilter(f.Args[0], n, m) || applyFilter(f.Args[1], n, m)

	case opVarIsStringLit:
		return checkBasicLit(getMatchExpr(m, f.Str), token.STRING)
	case opVarIsRuneLit:
		return checkBasicLit(getMatchExpr(m, f.Str), token.CHAR)
	case opVarIsIntLit:
		return checkBasicLit(getMatchExpr(m, f.Str), token.INT)
	case opVarIsFloatLit:
		return checkBasicLit(getMatchExpr(m, f.Str), token.FLOAT)
	case opVarIsComplexLit:
		return checkBasicLit(getMatchExpr(m, f.Str), token.IMAG)

	case opVarIsConst:
		v, ok := m.CapturedByName(f.Str)
		if !ok {
			return false
		}
		if e, ok := v.(ast.Expr); ok {
			return isConstExpr(e)
		}
		return false

	case opVarIsPure:
		v, ok := m.CapturedByName(f.Str)
		if !ok {
			return false
		}
		if e, ok := v.(ast.Expr); ok {
			return isPureExpr(e)
		}
		return false

	default:
		fmt.Fprintf(os.Stderr, "can't handle %v\n", f)
	}

	return true
}

func isPureExpr(expr ast.Expr) bool {
	// This list switch is not comprehensive and uses
	// whitelist to be on the conservative side.
	// Can be extended as needed.

	if expr == nil {
		return true
	}

	switch expr := expr.(type) {
	case *ast.StarExpr:
		return isPureExpr(expr.X)
	case *ast.BinaryExpr:
		return isPureExpr(expr.X) &&
			isPureExpr(expr.Y)
	case *ast.UnaryExpr:
		return expr.Op != token.ARROW &&
			isPureExpr(expr.X)
	case *ast.BasicLit, *ast.Ident:
		return true
	case *ast.SliceExpr:
		return isPureExpr(expr.X) &&
			isPureExpr(expr.Low) &&
			isPureExpr(expr.High) &&
			isPureExpr(expr.Max)
	case *ast.IndexExpr:
		return isPureExpr(expr.X) &&
			isPureExpr(expr.Index)
	case *ast.SelectorExpr:
		return isPureExpr(expr.X)
	case *ast.ParenExpr:
		return isPureExpr(expr.X)
	case *ast.TypeAssertExpr:
		return isPureExpr(expr.X)
	case *ast.CompositeLit:
		return isPureExprList(expr.Elts)

	case *ast.CallExpr:
		ident, ok := expr.Fun.(*ast.Ident)
		if !ok {
			return false
		}
		switch ident.Name {
		case "len", "cap", "real", "imag":
			return isPureExprList(expr.Args)
		default:
			return false
		}

	default:
		return false
	}
}

func isPureExprList(list []ast.Expr) bool {
	for _, expr := range list {
		if !isPureExpr(expr) {
			return false
		}
	}
	return true
}

func isConstExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BasicLit:
		return true
	case *ast.UnaryExpr:
		return isConstExpr(e.X)
	case *ast.BinaryExpr:
		return isConstExpr(e.X) && isConstExpr(e.Y)
	default:
		return false
	}
}

var badExpr = &ast.BadExpr{}

func getMatchExpr(m gogrep.MatchData, name string) ast.Expr {
	n, ok := m.CapturedByName(name)
	if !ok {
		return badExpr
	}
	e, ok := n.(ast.Expr)
	if !ok {
		return badExpr
	}
	return e
}

func checkBasicLit(n ast.Expr, kind token.Token) bool {
	if lit, ok := n.(*ast.BasicLit); ok {
		return lit.Kind == kind
	}
	return false
}
