package filters

import (
	"fmt"
	"math"
	"strings"
)

type SpecialPredicate struct {
	Name    string
	Negated bool
}

type Info struct {
	FilePredicates     []SpecialPredicate
	FunctionPredicates []SpecialPredicate

	Vars []string

	OpTab *OperationsTable
}

func IsRootVarname(varname string) bool {
	return varname == dollardollarVar
}

func (info *Info) String() string {
	var parts []string
	for _, f := range info.FilePredicates {
		negate := ""
		if f.Negated {
			negate = "!"
		}
		parts = append(parts, negate+"file."+f.Name+"()")
	}
	for _, f := range info.FunctionPredicates {
		negate := ""
		if f.Negated {
			negate = "!"
		}
		parts = append(parts, negate+"function."+f.Name+"()")
	}
	for _, varname := range info.Vars {
		parts = append(parts, "$"+varname)
	}
	return strings.Join(parts, " ")
}

type Expr struct {
	Op   Operation
	Num  int32
	Args []*Expr
	Str  string
}

type OperationsTable struct {
	opByVarFunc map[string]Operation
	nameByOp    map[Operation]string
}

func NewOperationTable(varFuncs map[string]Operation) *OperationsTable {
	tab := &OperationsTable{
		opByVarFunc: make(map[string]Operation),
		nameByOp:    make(map[Operation]string),
	}
	for funcName, op := range varFuncs {
		tab.opByVarFunc[funcName] = op
		tab.nameByOp[op] = funcName
	}
	return tab
}

func Parse(tab *OperationsTable, s string) (*Expr, Info, error) {
	p := filterParser{tab: tab}
	return p.Parse(s)
}

func Sprint(info *Info, e *Expr) string {
	parts := make([]string, 0, len(e.Args))
	if e.Str != "" {
		parts = append(parts, fmt.Sprintf("%q", e.Str))
	}
	for _, arg := range e.Args {
		parts = append(parts, Sprint(info, arg))
	}
	opString := ""
	if e.Op.IsBuiltin() {
		opString = e.Op.String()
	} else {
		opString = "%" + info.OpTab.nameByOp[e.Op]
	}
	if len(parts) == 0 {
		return opString
	}
	return "(" + opString + " " + strings.Join(parts, " ") + ")"
}

func Walk(e *Expr, callback func(e *Expr) bool) {
	if !callback(e) {
		return
	}
	for _, arg := range e.Args {
		if !callback(arg) {
			return
		}
	}
}

//go:generate stringer -type=Operation -trimprefix=Op
type Operation uint32

func (op Operation) IsBuiltin() bool {
	return op > opLastBuiltin || op == OpInvalid
}

const (
	OpInvalid Operation = 0

	// OpNop = do nothing (should be optimized-away, unless it's a top level op)
	OpNop Operation = math.MaxUint32 - iota

	// OpString is a string literal that holds the value inside $Str.
	OpString

	// OpNot = !$Args[0]
	OpNot

	// OpAnd = $Args[0] && $Args[1]
	OpAnd

	// OpOr = $Args[0] || $Args[1]
	OpOr

	// OpEq = $Args[0] == $Args[1]
	OpEq

	// OpNotEq = $Args[0] != $Args[1]
	OpNotEq

	// OpFunctionVarFunc = function.$Str()
	OpFunctionVarFunc

	opLastBuiltin
)
