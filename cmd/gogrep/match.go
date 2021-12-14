package main

type match struct {
	text             string
	matchStartOffset int
	matchLength      int

	filename string
	line     int
	startPos int
	endPos   int
}
