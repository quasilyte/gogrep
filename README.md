![logo](https://github.com/quasilyte/vscode-gogrep/blob/master/docs/logo.png?raw=true)

![Build Status](https://github.com/quasilyte/gogrep/workflows/Go/badge.svg)
[![PkgGoDev](https://pkg.go.dev/badge/mod/github.com/quasilyte/gogrep)](https://pkg.go.dev/github.com/quasilyte/gogrep)
[![Go Report Card](https://goreportcard.com/badge/github.com/quasilyte/gogrep)](https://goreportcard.com/report/github.com/quasilyte/gogrep)
![Code Coverage](https://codecov.io/gh/quasilyte/gogrep/branch/master/graph/badge.svg)

# gogrep

WIP: this is an attempt to move modified [gogrep](https://github.com/mvdan/gogrep) from the [go-ruleguard](https://github.com/quasilyte/go-ruleguard) project, so it can be used outside of the ruleguard as a library.

## Used by

gogrep as a library is used by:

* [go-ruleguard](https://github.com/quasilyte/go-ruleguard)
* [gocorpus](https://github.com/quasilyte/gocorpus)

## Acknowledgements

The original gogrep is written by the [Daniel Martí](https://github.com/mvdan).

## Command line arguments

### `-v` argument

Output additional verbose information about execution process. Disabled by default.

### `-limit` argument

By default, `gogrep` stops when it finds 1000 matches.

This is due to the fact that it's easy to write a too generic pattern and get overwhelming results on the big code
bases.

To increase this limit, specify the `-limit` argument with some big value you're comfortable with.

If you want to set it to the max value, use `-limit 0`.

NOTE: Max limit for not `count mode` 100k results and for `count mode` it limited by max uint64 value.

### `-workers` argument

Set the number of concurrent workers. By default, equal to number of logical CPUs usable by the current process.

### `-memprofile` argument

Write memory profile to the specified file. By default, memory profile is not collected.

NOTE: Collect GC usage and current runtime heap profile.

### `-cpuprofile` argument

Write cpu profile to the specified file. By default, cpu profile is not collected.

### `-strict-syntax` argument

When strict is false, gogrep may consider 0xA and 10 to be identical. By default, strict-syntax is disabled.

### `-exclude` argument

If you want to ignore some directories or files, use `-exclude` argument. Argument accepts a regexp argument.

Here is an example that ignores everything under the `node_modules` folder:

```bash
$ gogrep -exclude '/node_modules$' . '<pattern>'
```

### `-progress` argument

In order to work faster, `gogrep` doesn't print any search results until it finds them all (or reaches the `-limit`).

If you're searching through a big (several millions SLOC) project, it could take a few seconds to complete. As it might
look like the program hangs, `gogrep` prints its progress in this manner:

```
N matches so far, processed M files
```

Where `N` and `M` are variables that will change over time.

`gogrep` has 3 progress reporting modes:

* `none` which is a silent mode, no progress will be printed
* `append` is a simplest mode that just writes one log message after another
* `update` is a more user-friendly mode that will use `\r` to update the message (default)

To override the default mode, use `-progress` argument.

> Note: logs are written to the `stderr` while matches are written to the `stdout`.

### `-format` argument

Sometimes you want to print the result in some specific way.

Suppose that we have this `target.go` file:

```go
func f() {
    panic("unimplemented") // Should never happen
}
```

```bash
$ gogrep target.go 'panic($_)'
target.go:3:     panic("unimplemented") // Should never happen
```

As you see, it prints the whole line and the match location, not just the match. If we want `gogrep` to output only the
matched part, we can use the `-format` flag.

```bash
$ gogrep -format '{{.Match}}' target.go 'panic($_)'
panic("unimplemented")
```

The default format value is `{{.Filename}}:{{.Line}}: {{.MatchLine}}`.

Several template variables are available:

```
  {{.Filename}}  match containing file name
  {{.Line}}      line number where the match started
  {{.MatchLine}} a source code line that contains the match
  {{.Match}}     an entire match string
  {{.x}}         $x submatch string (can be any submatch name)
```

### `-heatmap` argument

A CPU profile that will be used to build a heatmap, needed for `IsHot()` filters. By default, heatmap profile is not collected.

### `-heatmap-threshold` argument

A threshold argument used to create a heatmap, see perf-heatmap [docs](https://github.com/quasilyte/perf-heatmap) on it. By default value equal to `0.5`.

### Count mode, `-c` argument

Count mode discards all match data, but prints the total matches count to the `stderr`. Disabled by default.

### `-abs` argument

By default, `gogrep` prints the relative filenames in the output.

Technically speaking, it sets `{{.Filename}}` variable to the relative file name.

To get an absolute path there, use `-abs` argument.

```bash
$ gogrep target.go 'append($_, $_)'
target.go:3:     append($data, $elem)

$ gogrep -abs target.go 'append($_, $_)'
/home/quasilyte/target.go:3:     append($data, $elem)
```

### Multi-line mode, `-m` argument

Some patterns may match a code that spans across the multiple lines.

By default, `gogrep` replaces all newlines with `\n` sequence, so you can treat all matches as strings without newlines.

If you want to avoid that behavior, `-m` argument can be used.

```go
// target.go
println(
    1,
    2,
)
```

```bash
$ gogrep target.go 'println(${"*"})'
target.go:2: println(\n    1,\n    2\n)

$ gogrep -m target.go 'println(${"*"})'
target.go:2: println(
    1,
    2,
)
```

### `-no-calor` argument

`gogrep` inserts ANSI color escapes by the default.

You can disable this behavior with the `-no-color` flag. 

### `-color-filename` argument

Specify color scheme for {{.Filename}} format variable. By default, `dark-magenta` color used.

NOTE: this argument also can be specified by `GOGREP_COLOR_FILENAME` environment variable.

### `-color-line` argument

Specify color scheme for {{.Line}} format variable. By default, `dark-green` color used.

NOTE: this argument also can be specified by `GOGREP_COLOR_LINE` environment variable.

### `-color-match` argument

Specify color scheme for {{.Match}} format variable. By default, `dark-red` color used.

NOTE: this argument also can be specified by `GOGREP_COLOR_MATCH` environment variable.
