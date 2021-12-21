package filters

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

const mangledPatternVar = "__vAR_"
const dollardollarVar = "_Dollar2_"

func preprocess(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "$$", mangledPatternVar+dollardollarVar)
	return strings.ReplaceAll(s, "$", mangledPatternVar)
}

func isPatternVar(s string) bool { return strings.HasPrefix(s, mangledPatternVar) }

func patternVarName(s string) string { return strings.TrimPrefix(s, mangledPatternVar) }

type filterParser struct {
	info Info

	tab *OperationsTable

	varnameToID       map[string]int32
	functionFilterSet map[SpecialPredicate]struct{}

	insideOr  bool
	insideNot bool
}

func (p *filterParser) Parse(s string) (*Expr, Info, error) {
	s = preprocess(s)
	if s == "" {
		return &Expr{Op: OpNop}, p.info, nil
	}

	root, err := parser.ParseExpr(s)
	if err != nil {
		return nil, Info{}, err
	}

	p.varnameToID = make(map[string]int32)
	p.functionFilterSet = make(map[SpecialPredicate]struct{})

	p.info.OpTab = p.tab

	e, err := p.convertExpr(root)
	if err != nil {
		return nil, p.info, err
	}
	e = p.removeNopRecursive(e)

	return e, p.info, nil
}

func (p *filterParser) internVar(varname string) int32 {
	id, ok := p.varnameToID[varname]
	if !ok {
		id = int32(len(p.info.Vars))
		p.info.Vars = append(p.info.Vars, varname)
		p.varnameToID[varname] = id
	}
	return id
}

func (p *filterParser) removeNopRecursive(e *Expr) *Expr {
	for i, arg := range e.Args {
		e.Args[i] = p.removeNopRecursive(arg)
	}
	switch e.Op {
	case OpAnd:
		if e.Args[0].Op == OpNop {
			*e = *e.Args[1]
		} else if e.Args[1].Op == OpNop {
			*e = *e.Args[0]
		}
	case OpNot:
		if e.Args[0].Op == OpNop {
			e.Op = OpNop
			e.Args = nil
		}
	}
	return e
}

func (p *filterParser) convertExpr(root ast.Expr) (*Expr, error) {
	switch root := root.(type) {
	case *ast.UnaryExpr:
		return p.convertUnaryExpr(root)
	case *ast.BinaryExpr:
		return p.convertBinaryExpr(root)
	case *ast.ParenExpr:
		return p.convertExpr(root.X)
	case *ast.CallExpr:
		return p.convertCallExpr(root)
	case *ast.BasicLit:
		return p.convertBasicLit(root)
	default:
		return nil, fmt.Errorf("convert expr: unsupported %T", root)
	}
}

func (p *filterParser) convertBasicLit(root *ast.BasicLit) (*Expr, error) {
	switch root.Kind {
	case token.STRING:
		val, err := strconv.Unquote(root.Value)
		return &Expr{Op: OpString, Str: val}, err
	default:
		return nil, fmt.Errorf("convert basic lit: unsupported %s", root.Kind)
	}
}

func (p *filterParser) convertCallExpr(root *ast.CallExpr) (*Expr, error) {
	if selector, ok := root.Fun.(*ast.SelectorExpr); ok {
		return p.convertMethodCallExpr(root, selector)
	}
	return nil, fmt.Errorf("convert call expr: unsupported %v function", root.Fun)
}

func (p *filterParser) convertMethodCallExpr(root *ast.CallExpr, selector *ast.SelectorExpr) (*Expr, error) {
	var object string
	ident, ok := selector.X.(*ast.Ident)
	if ok {
		object = ident.Name
	}

	switch object {
	case "file":
		return p.convertFileMethodCallExpr(root, selector.Sel)
	case "function":
		return p.convertFunctionMethodCallExpr(root, selector.Sel)
	default:
		if isPatternVar(object) {
			varName := patternVarName(object)
			id := p.internVar(varName)
			op, ok := p.tab.opByVarFunc[selector.Sel.Name]
			if ok {
				e := &Expr{Op: op, Num: id, Str: varName}
				return e, nil
			}
			return nil, fmt.Errorf("convert method expr: unsupported %s method", selector.Sel.Name)
		}
		return nil, fmt.Errorf("convert method expr: unsupported %T object", selector.X)
	}
}

func (p *filterParser) convertFunctionMethodCallExpr(root *ast.CallExpr, method *ast.Ident) (*Expr, error) {
	if !p.insideOr {
		f := SpecialPredicate{Name: method.Name, Negated: p.insideNot}
		if _, ok := p.functionFilterSet[f]; !ok {
			p.functionFilterSet[f] = struct{}{}
			p.info.FunctionPredicates = append(p.info.FunctionPredicates, f)
		}
	}
	return &Expr{Op: OpFunctionVarFunc, Str: method.Name}, nil
}

func (p *filterParser) convertFileMethodCallExpr(root *ast.CallExpr, method *ast.Ident) (*Expr, error) {
	if p.insideOr {
		return nil, fmt.Errorf("file filters can't be a part of || expression")
	}
	f := SpecialPredicate{Name: method.Name, Negated: p.insideNot}
	p.info.FilePredicates = append(p.info.FilePredicates, f)
	return &Expr{Op: OpNop}, nil
}

func (p *filterParser) convertUnaryExpr(root *ast.UnaryExpr) (*Expr, error) {
	switch root.Op {
	case token.NOT:
		insideNot := p.insideNot
		p.insideNot = !insideNot
		x, err := p.convertExpr(root.X)
		p.insideNot = insideNot
		if err != nil {
			return nil, err
		}
		return &Expr{Op: OpNot, Args: []*Expr{x}}, nil

	default:
		return nil, fmt.Errorf("convert unary expr: unsupported %s", root.Op)
	}
}

func (p *filterParser) convertBinaryExpr(root *ast.BinaryExpr) (*Expr, error) {
	if _, ok := root.X.(*ast.BasicLit); ok {
		if _, ok := root.Y.(*ast.BasicLit); !ok {
			switch root.Op {
			case token.GEQ:
				return p.convertBinaryExprXY(token.LEQ, root.Y, root.X)
			case token.LEQ:
				return p.convertBinaryExprXY(token.GEQ, root.Y, root.X)
			case token.GTR:
				return p.convertBinaryExprXY(token.LSS, root.Y, root.X)
			case token.LSS:
				return p.convertBinaryExprXY(token.GTR, root.Y, root.X)
			case token.EQL:
				return p.convertBinaryExprXY(token.EQL, root.Y, root.X)
			case token.NEQ:
				return p.convertBinaryExprXY(token.NEQ, root.Y, root.X)
			}
		}
	}

	return p.convertBinaryExprXY(root.Op, root.X, root.Y)
}

func (p *filterParser) convertBinaryExprXY(op token.Token, x, y ast.Expr) (*Expr, error) {
	if op == token.LOR {
		insideOr := p.insideOr
		p.insideOr = true
		lhs, err := p.convertExpr(x)
		if err != nil {
			return nil, err
		}
		rhs, err := p.convertExpr(y)
		if err != nil {
			return nil, err
		}
		p.insideOr = insideOr
		return &Expr{Op: OpOr, Args: []*Expr{lhs, rhs}}, nil
	}

	lhs, err := p.convertExpr(x)
	if err != nil {
		return nil, err
	}
	rhs, err := p.convertExpr(y)
	if err != nil {
		return nil, err
	}

	switch op {
	case token.LAND:
		return &Expr{Op: OpAnd, Args: []*Expr{lhs, rhs}}, nil

	case token.EQL:
		return &Expr{Op: OpEq, Args: []*Expr{lhs, rhs}}, nil
	case token.NEQ:
		return &Expr{Op: OpNotEq, Args: []*Expr{lhs, rhs}}, nil
	}

	return nil, fmt.Errorf("convert binary expr: unsupported %s", op)
}
