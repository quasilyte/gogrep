package main

import (
	"github.com/quasilyte/gogrep"
)

type match struct {
	text             string
	matchStartOffset int
	matchLength      int

	capture []capturedNode

	filename    string
	line        int
	startOffset int
	endOffset   int
}

type capturedNode struct {
	startOffset int
	endOffset   int
	data        gogrep.CapturedNode
}
