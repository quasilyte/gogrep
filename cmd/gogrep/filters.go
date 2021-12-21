package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"

	"github.com/quasilyte/gogrep"
	"github.com/quasilyte/gogrep/filters"
	"github.com/quasilyte/perf-heatmap/heatmap"
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
	opVarText
	opVarIsHot
)

type filterContext struct {
	m gogrep.MatchData
	w *worker
}

func (ctx *filterContext) NodeText(varname string) []byte {
	n, ok := capturedByName(ctx.m, varname)
	if !ok {
		return nil
	}
	return ctx.w.nodeText(n)
}

func applyFilter(ctx filterContext, f *filters.Expr, n ast.Node) bool {
	switch f.Op {
	case filters.OpNot:
		return !applyFilter(ctx, f.Args[0], n)

	case filters.OpAnd:
		return applyFilter(ctx, f.Args[0], n) && applyFilter(ctx, f.Args[1], n)

	case filters.OpOr:
		return applyFilter(ctx, f.Args[0], n) || applyFilter(ctx, f.Args[1], n)

	case opVarIsStringLit:
		return checkBasicLit(getMatchExpr(ctx.m, f.Str), token.STRING)
	case opVarIsRuneLit:
		return checkBasicLit(getMatchExpr(ctx.m, f.Str), token.CHAR)
	case opVarIsIntLit:
		return checkBasicLit(getMatchExpr(ctx.m, f.Str), token.INT)
	case opVarIsFloatLit:
		return checkBasicLit(getMatchExpr(ctx.m, f.Str), token.FLOAT)
	case opVarIsComplexLit:
		return checkBasicLit(getMatchExpr(ctx.m, f.Str), token.IMAG)

	case opVarIsConst:
		v, ok := ctx.m.CapturedByName(f.Str)
		if !ok {
			return false
		}
		if e, ok := v.(ast.Expr); ok {
			return isConstExpr(e)
		}
		return false

	case opVarIsPure:
		v, ok := ctx.m.CapturedByName(f.Str)
		if !ok {
			return false
		}
		if e, ok := v.(ast.Expr); ok {
			return isPureExpr(e)
		}
		return false

	case opVarIsHot:
		v, ok := capturedByName(ctx.m, f.Str)
		if !ok {
			return false
		}
		lineFrom := ctx.w.fset.Position(v.Pos()).Line
		lineTo := ctx.w.fset.Position(v.End()).Line
		isHot := false
		key := heatmap.Key{
			TypeName: ctx.w.typeName,
			FuncName: ctx.w.funcName,
			Filename: filepath.Base(ctx.w.filename),
			PkgName:  ctx.w.pkgName,
		}
		if ctx.w.closureID != 0 {
			key.FuncName = key.FuncName + ".func" + strconv.Itoa(ctx.w.closureID)
		}
		ctx.w.heatmap.QueryLineRange(key, lineFrom, lineTo, func(line int, level heatmap.HeatLevel) bool {
			if level.Global != 0 {
				isHot = true
				return false
			}
			return true
		})
		return isHot

	case filters.OpEq:
		return applyEqFilter(ctx, f, n)
	case filters.OpNotEq:
		return !applyEqFilter(ctx, f, n)

	default:
		panic(fmt.Sprintf("can't handle %s\n", filters.Sprint(ctx.w.filterInfo, f)))
	}
}

func applyEqFilter(ctx filterContext, f *filters.Expr, n ast.Node) bool {
	x := f.Args[0]
	y := f.Args[1]
	if x.Op == opVarText {
		if y.Op == filters.OpString {
			return string(ctx.NodeText(x.Str)) == y.Str
		}
	}
	panic(fmt.Sprintf("can't handle %s\n", filters.Sprint(ctx.w.filterInfo, f)))
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
