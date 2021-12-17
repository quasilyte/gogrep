package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/token"
	"io"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/google/pprof/profile"
	"github.com/quasilyte/gogrep"
	"github.com/quasilyte/gogrep/filters"
	"github.com/quasilyte/perf-heatmap/heatmap"
)

// Following the grep tool convention.
const (
	exitMatched    = 0
	exitNotMatched = 1
	exitError      = 2
)

const defaultFormat = `{{.Filename}}:{{.Line}}: {{.MatchLine}}`

func main() {
	exitCode, err := mainNoExit()
	if err != nil {
		log.Printf("error: %+v", err)
		return
	}
	os.Exit(exitCode)
}

func mainNoExit() (int, error) {
	log.SetFlags(0)

	var args arguments
	parseFlags(&args)

	p := &program{
		args: args,
	}

	steps := []struct {
		name string
		fn   func() error
	}{
		{"validate flags", p.validateFlags},
		{"start profiling", p.startProfiling},
		{"load heatmap", p.loadHeatmap},
		{"compile filter", p.compileFilter},
		{"compile pattern", p.compilePattern},
		{"compile exclude pattern", p.compileExcludePattern},
		{"compile output format", p.compileOutputFormat},
		{"execute pattern", p.executePattern},
		{"print matches", p.printMatches},
		{"finish profiling", p.finishProfiling},
	}

	for _, step := range steps {
		if args.verbose {
			log.Printf("debug: starting %q step", step.name)
		}
		if err := step.fn(); err != nil {
			return exitError, fmt.Errorf("%s: %v", step.name, err)
		}
	}

	if p.numMatches == 0 {
		return exitNotMatched, nil
	}
	return exitMatched, nil
}

type arguments struct {
	abs          bool
	multiline    bool
	verbose      bool
	strictSyntax bool
	workers      uint
	limit        uint64

	format string

	countMode bool

	exclude      string
	progressMode string

	noColor       bool
	filenameColor string
	lineColor     string
	matchColor    string

	cpuProfile string
	memProfile string

	heatmapFile      string
	heatmapThreshold float64

	targets string
	pattern string
	filter  string
}

func parseFlags(args *arguments) {
	flag.Usage = func() {
		const usage = `Usage: gogrep [flags...] targets pattern [filter]
Where:
  flags are command-line arguments that are listed in -help (see below)
  targets is a comma-separated list of file or directory names to search in
  pattern is a string that describes what is being matched
  filter is Go expr string that can be used to reject certain matches
Examples:
  # Find f calls with a single argument.
  gogrep file.php 'f($_)'
  # Find any fmt.Println calls (any number of args).
  gogrep file.php 'fmt.Println($*_)
  # Run gogrep on 2 folders (recursively).
  gogrep dir1,dir2 '"some string"'
  # Run gogrep in src folder, ignoring all auto-generated files
  gogrep src 'os.Exit($_)' '!file.IsAutogen()'
  # Ignore third_party folder while searching.
  gogrep --exclude '/third_party/' project/ 'pattern'

The output colors can be configured with "--color-<name>" flags.
Use --no-color to disable the output coloring.

Exit status:
  0 if something is matched
  1 if nothing is matched
  2 if error occurred

For more info and examples visit https://github.com/quasilyte/gogrep

Supported command-line flags:
`
		fmt.Fprint(flag.CommandLine.Output(), usage)
		flag.PrintDefaults()
	}

	flag.BoolVar(&args.verbose, "v", false,
		`verbose mode: turn on additional debug logging`)
	flag.Uint64Var(&args.limit, "limit", 1000,
		`stop after this many match results, 0 for unlimited`)
	flag.UintVar(&args.workers, "workers", uint(runtime.NumCPU()),
		`set the number of concurrent workers`)
	flag.StringVar(&args.memProfile, "memprofile", "",
		`write memory profile to the specified file`)
	flag.StringVar(&args.cpuProfile, "cpuprofile", "",
		`write CPU profile to the specified file`)

	flag.BoolVar(&args.strictSyntax, "strict-syntax", false,
		`disable syntax normalizations, so 10 and 0xA are not considered to be identical, and so on`)
	flag.StringVar(&args.exclude, "exclude", "",
		`exclude files or directories by regexp pattern`)
	flag.StringVar(&args.progressMode, "progress", "update",
		`progress printing mode: "update", "append" or "none"`)
	flag.StringVar(&args.format, "format", defaultFormat,
		`specify an alternate format for the output, using the syntax Go templates`)

	flag.StringVar(&args.heatmapFile, "heatmap", "",
		`a CPU profile that will be used to build a heatmap, needed for IsHot() filters`)
	flag.Float64Var(&args.heatmapThreshold, "heatmap-threshold", 0.5,
		`a threshold argument used to create a heatmap, see perf-heatmap docs on it`)

	flag.BoolVar(&args.countMode, "c", false,
		`count mode that discards all match data, but prints the total matches count`)

	flag.BoolVar(&args.abs, "abs", false,
		`print absolute filenames in the output`)
	flag.BoolVar(&args.multiline, "m", false,
		`multiline mode: print matches without escaping newlines to \n`)

	flag.BoolVar(&args.noColor, "no-color", false,
		`disable colored output`)
	flag.StringVar(&args.filenameColor, "color-filename", envVarOrDefault("GOGREP_COLOR_FILENAME", "dark-magenta"),
		`{{.Filename}} text color, can also override via $GOGREP_COLOR_FILENAME`)
	flag.StringVar(&args.lineColor, "color-line", envVarOrDefault("GOGREP_COLOR_LINE", "dark-green"),
		`{{.Line}} text color, can also override via $GOGREP_COLOR_LINE`)
	flag.StringVar(&args.matchColor, "color-match", envVarOrDefault("GOGREP_COLOR_MATCH", "dark-red"),
		`{{.Match}} text color, can also override via $GOGREP_COLOR_MATCH`)

	flag.Parse()

	argv := flag.Args()
	if len(argv) != 0 {
		args.targets = argv[0]
	}
	if len(argv) >= 2 {
		args.pattern = argv[1]
	}
	if len(argv) >= 3 {
		args.filter = argv[2]
	}

	if args.verbose {
		log.Printf("debug: targets: %s", args.targets)
		log.Printf("debug: pattern: %s", args.pattern)
		log.Printf("debug: filter: %s", args.filter)
	}
}

type program struct {
	args arguments

	numMatches uint64

	exclude *regexp.Regexp

	heatmap *heatmap.Index

	filterHints filterHints
	filterInfo  filters.Info
	filterExpr  *filters.Expr

	workers []*worker

	outputTemplate *template.Template

	cpuProfile bytes.Buffer
}

func (p *program) validateFlags() error {
	workersLimit := uint(runtime.NumCPU() * 4)
	if p.args.workers > workersLimit {
		p.args.workers = workersLimit
	}

	if p.args.targets == "" {
		return fmt.Errorf("target can't be empty")
	}
	if p.args.pattern == "" {
		return fmt.Errorf("pattern can't be empty")
	}

	if _, err := colorizeText("", p.args.filenameColor); err != nil {
		return fmt.Errorf("color-filename: %v", err)
	}
	if _, err := colorizeText("", p.args.lineColor); err != nil {
		return fmt.Errorf("color-line: %v", err)
	}
	if _, err := colorizeText("", p.args.matchColor); err != nil {
		return fmt.Errorf("color-match: %v", err)
	}

	switch p.args.progressMode {
	case "none", "append", "update":
		// OK.
	default:
		return fmt.Errorf("progress: unexpected mode %q", p.args.progressMode)
	}

	if p.args.countMode {
		if p.args.limit == 0 {
			p.args.limit = math.MaxUint64
		}
	} else {
		// If there are more than 100k results, something is wrong.
		// Most likely, a user pattern is too generic and needs adjustment.
		const maxLimit = 100000
		if p.args.limit == 0 || p.args.limit > maxLimit {
			p.args.limit = maxLimit
		}
	}

	return nil
}

func (p *program) startProfiling() error {
	if p.args.cpuProfile == "" {
		return nil
	}

	if err := pprof.StartCPUProfile(&p.cpuProfile); err != nil {
		return fmt.Errorf("could not start CPU profile: %v", err)
	}

	return nil
}

func (p *program) loadHeatmap() error {
	if p.args.heatmapFile == "" {
		return nil
	}

	data, err := os.ReadFile(p.args.heatmapFile)
	if err != nil {
		return err
	}
	pprofProfile, err := profile.Parse(bytes.NewReader(data))
	if err != nil {
		return err
	}
	config := heatmap.IndexConfig{
		Threshold: p.args.heatmapThreshold,
	}
	index := heatmap.NewIndex(config)
	if err := index.AddProfile(pprofProfile); err != nil {
		return err
	}

	p.heatmap = index

	return nil
}

func (p *program) compileFilter() error {
	varOps := map[string]filters.Operation{
		"IsPure":       opVarIsPure,
		"IsConst":      opVarIsConst,
		"IsStringLit":  opVarIsStringLit,
		"IsRuneLit":    opVarIsRuneLit,
		"IsIntLit":     opVarIsIntLit,
		"IsFloatLit":   opVarIsFloatLit,
		"IsComplexLit": opVarIsComplexLit,
		"IsHot":        opVarIsHot,
	}
	optab := filters.NewOperationTable(varOps)
	expr, info, err := filters.Parse(optab, p.args.filter)
	if err != nil {
		return err
	}
	for _, pred := range info.FilePredicates {
		switch pred.Name {
		case "IsAutogen":
			p.filterHints.autogenCond = newBool3(!pred.Negated)
		default:
			return fmt.Errorf("unsupported file predicate: %s", pred.Name)
		}
	}
	p.filterInfo = info
	p.filterExpr = expr
	return nil
}

func (p *program) compilePattern() error {
	fset := token.NewFileSet()
	config := gogrep.CompileConfig{
		Fset:      fset,
		Src:       p.args.pattern,
		Strict:    p.args.strictSyntax,
		WithTypes: false,
	}
	m, _, err := gogrep.Compile(config)
	if err != nil {
		return err
	}

	workDir, err := os.Getwd()
	if err != nil {
		return err
	}

	p.workers = make([]*worker, p.args.workers)
	for i := range p.workers {
		p.workers[i] = &worker{
			workDir:     workDir,
			heatmap:     p.heatmap,
			filterHints: p.filterHints,
			filterInfo:  &p.filterInfo,
			filterExpr:  p.filterExpr,
			id:          i,
			m:           m.Clone(),
			countMode:   p.args.countMode,
		}
	}

	return nil
}

func (p *program) compileExcludePattern() error {
	if p.args.exclude == "" {
		return nil
	}
	var err error
	p.exclude, err = regexp.Compile(p.args.exclude)
	if err != nil {
		return fmt.Errorf("invalid exclude regexp: %v", err)
	}
	return nil
}

func (p *program) compileOutputFormat() error {
	format := p.args.format
	var err error
	p.outputTemplate, err = template.New("output-format").Parse(format)
	if err != nil {
		return err
	}
	return nil
}

func (p *program) executePattern() error {
	filenameQueue := make(chan string)
	ticker := time.NewTicker(time.Second)

	var wg sync.WaitGroup
	wg.Add(len(p.workers))
	defer func() {
		close(filenameQueue)
		ticker.Stop()
		wg.Wait()
		if p.args.progressMode == "update" {
			// Clear the line so the progress text doesn't clutter the following output.
			os.Stderr.WriteString("\r\033[K")
		}
		for _, w := range p.workers {
			for _, err := range w.errors {
				log.Print(err)
			}
		}
	}()

	for _, w := range p.workers {
		go func(w *worker) {
			defer wg.Done()

			for filename := range filenameQueue {
				if p.args.verbose {
					log.Printf("debug: worker#%d greps %q file", w.id, filename)
				}

				numMatches, err := w.grepFile(filename)
				if err != nil {
					msg := fmt.Sprintf("error: execute pattern: %s: %v", filename, err)
					if p.args.progressMode == "update" {
						w.errors = append(w.errors, msg)
					} else {
						log.Print(msg)
					}
					continue
				}
				if numMatches == 0 {
					continue
				}

				atomic.AddUint64(&p.numMatches, uint64(numMatches))
			}
		}(w)
	}

	for _, target := range strings.Split(p.args.targets, ",") {
		target = strings.TrimSpace(target)
		if err := p.walkTarget(target, filenameQueue, ticker); err != nil {
			return err
		}
	}

	return nil
}

func (p *program) walkTarget(target string, filenameQueue chan<- string, ticker *time.Ticker) error {
	// TODO: skip some dirs like node_modules, .git and so on?
	filesProcessed := 0
	err := filepath.WalkDir(target, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		numMatches := atomic.LoadUint64(&p.numMatches)
		if numMatches > p.args.limit {
			return io.EOF
		}

		if p.exclude != nil {
			fullName, err := filepath.Abs(path)
			if err != nil {
				log.Printf("error: abs(%s): %v", path, err)
			}
			skip := p.exclude.MatchString(fullName)
			if skip && info.IsDir() {
				return filepath.SkipDir
			}
			if skip {
				return nil
			}
		}

		if info.IsDir() {
			return nil
		}
		if !isGoFilename(info.Name()) {
			return nil
		}

		for {
			select {
			case filenameQueue <- path:
				filesProcessed++
				return nil
			case <-ticker.C:
				switch p.args.progressMode {
				case "append":
					fmt.Fprintf(os.Stderr, "%d matches so far, processed %d files\n", numMatches, filesProcessed)
				case "update":
					fmt.Fprintf(os.Stderr, "\r%d matches so far, processed %d files", numMatches, filesProcessed)
				case "none":
					// Do nothing.
				}
			}
		}
	})
	if err == io.EOF {
		return nil
	}

	return err
}

func (p *program) printMatches() error {
	if p.args.countMode {
		log.Printf("found %d matches", p.numMatches)
		return nil
	}

	printed := uint64(0)
	for _, w := range p.workers {
		for _, m := range w.matches {
			if err := printMatch(p.outputTemplate, &p.args, m); err != nil {
				return err
			}
			printed++
			if printed >= p.args.limit {
				log.Printf("results limited to %d matches", p.args.limit)
				return nil
			}
		}
	}
	log.Printf("found %d matches", printed)
	return nil
}

func (p *program) finishProfiling() error {
	if p.args.cpuProfile != "" {
		pprof.StopCPUProfile()
		err := os.WriteFile(p.args.cpuProfile, p.cpuProfile.Bytes(), 0o600)
		if err != nil {
			return fmt.Errorf("write CPU profile: %v", err)
		}
	}

	if p.args.memProfile != "" {
		f, err := os.Create(p.args.memProfile)
		if err != nil {
			return fmt.Errorf("create mem profile: %v", err)
		}
		defer f.Close()
		runtime.GC() // get up-to-date statistics
		if err := pprof.WriteHeapProfile(f); err != nil {
			return fmt.Errorf("write mem profile: %v", err)
		}
	}

	return nil
}

func printMatch(tmpl *template.Template, args *arguments, m match) error {
	s, err := renderTemplate(m, renderConfig{
		tmpl:        tmpl,
		colors:      !args.noColor,
		multiline:   args.multiline,
		absFilename: args.abs,
		args:        args,
	})
	if err != nil {
		return err
	}
	fmt.Println(s)
	return nil
}

type renderConfig struct {
	tmpl        *template.Template
	colors      bool
	multiline   bool
	absFilename bool
	args        *arguments
}

func renderTemplate(m match, config renderConfig) (string, error) {
	matchText := m.text[m.matchStartOffset : m.matchStartOffset+m.matchLength]
	filename := m.filename
	if config.absFilename {
		abs, err := filepath.Abs(filename)
		if err != nil {
			return "", fmt.Errorf("abs(%q): %v", m.filename, err)
		}
		filename = abs
	}

	data := make(map[string]interface{}, 4)

	// Assign these after the captures so they overwrite them in case of collisions.
	data["Filename"] = filename
	data["Line"] = m.line
	data["Match"] = matchText
	data["MatchLine"] = m.text

	if config.colors {
		data["Filename"] = mustColorizeText(filename, config.args.filenameColor)
		data["Line"] = mustColorizeText(fmt.Sprint(m.line), config.args.lineColor)
		data["Match"] = mustColorizeText(matchText, config.args.matchColor)
		data["MatchLine"] = m.text[:m.matchStartOffset] + mustColorizeText(matchText, config.args.matchColor) + m.text[m.matchStartOffset+m.matchLength:]
	}

	if !config.multiline {
		data["Match"] = strings.ReplaceAll(data["Match"].(string), "\n", `\n`)
		data["MatchLine"] = strings.ReplaceAll(data["MatchLine"].(string), "\n", `\n`)
	}

	var buf strings.Builder
	buf.Grow(len(data["MatchLine"].(string)) * 2) // Approx
	if err := config.tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
