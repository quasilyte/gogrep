package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"

	"github.com/quasilyte/gogrep"
)

type worker struct {
	id int

	countMode bool

	m           *gogrep.Pattern
	gogrepState gogrep.MatcherState
	fset        *token.FileSet

	matches []match

	errors []string

	data     []byte
	filename string
	n        int
}

func (w *worker) grepFile(filename string) (int, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return 0, fmt.Errorf("read file: %v", err)
	}

	w.fset = token.NewFileSet()
	root, err := w.parseFile(w.fset, filename, data)
	if err != nil {
		return 0, err
	}

	w.data = data
	w.filename = filename
	w.n = 0
	ast.Inspect(root, w.Visit)
	return w.n, nil
}

func (w *worker) parseFile(fset *token.FileSet, filename string, data []byte) (*ast.File, error) {
	f, err := parser.ParseFile(fset, filename, data, 0)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (w *worker) Visit(n ast.Node) bool {
	w.m.MatchNode(&w.gogrepState, n, func(data gogrep.MatchData) {
		w.n++

		if w.countMode {
			return
		}

		start := w.fset.Position(data.Node.Pos())
		end := w.fset.Position(data.Node.End())
		m := match{
			filename: w.filename,
			line:     start.Line,
			startPos: start.Offset,
			endPos:   end.Offset,
		}
		w.initMatchText(&m, start.Offset, end.Offset)
		w.matches = append(w.matches, m)
	})

	return true
}

func (w *worker) initMatchText(m *match, startPos, endPos int) {
	isNewline := func(b byte) bool {
		return b == '\n' || b == '\r'
	}

	start := startPos
	for start > 0 {
		if isNewline(w.data[start]) {
			if start != startPos {
				start++
			}
			break
		}
		start--
	}
	end := endPos
	for end < len(w.data) {
		if isNewline(w.data[end]) {
			break
		}
		end++
	}

	m.text = string(w.data[start:end])
	m.matchStartOffset = startPos - start
	m.matchLength = endPos - startPos
}
