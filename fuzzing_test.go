//go:build go1.18
// +build go1.18

package gogrep

import (
	"go/token"
	"testing"
)

func FuzzPatternParsing(f *testing.F) {
	seeds := []string{
		// Good patterns.
		"f()",
		"x + y",
		"$x()",
		"$_($*_)",
		"fmt.Sprintf($format, $*args)",
		"copy($x, $x)",
		"[]rune($s)",

		// Bad patterns.
		`()`,
		`=0`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		defer func() {
			rv := recover()
			if rv != nil {
				t.Fatalf("panic during compiling %q", s)
			}
		}()
		_, _, _ = Compile(CompileConfig{
			Fset: token.NewFileSet(),
			Src:  s,
		})
	})
}
