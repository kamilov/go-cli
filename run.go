package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
)

type runOptions struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	lookup func(string) (string, bool)
}

// RunOption sets a parameter of one Run or Complete call. Nothing set by it
// outlives the call.
type RunOption func(*runOptions)

// WithStdin sets the stdin returned by Stdin. The default is os.Stdin.
func WithStdin(r io.Reader) RunOption { return func(o *runOptions) { o.stdin = r } }

// WithStdout sets the stdout used for help, version and Stdout. The default
// is os.Stdout.
func WithStdout(w io.Writer) RunOption { return func(o *runOptions) { o.stdout = w } }

// WithStderr sets the stderr used for warnings and Stderr. The default is
// os.Stderr.
func WithStderr(w io.Writer) RunOption { return func(o *runOptions) { o.stderr = w } }

// WithEnv sets the lookup of environment variables. The default is
// os.LookupEnv.
func WithEnv(lookup func(name string) (string, bool)) RunOption {
	return func(o *runOptions) { o.lookup = lookup }
}

// runState is the state of one call, carried by the context.
type runState struct {
	runOptions
	chain []*Command
}

type stateKey struct{}

func newState(opts []RunOption) *runState {
	st := &runState{}
	for _, o := range opts {
		if o != nil {
			o(&st.runOptions)
		}
	}
	if st.stdin == nil {
		st.stdin = os.Stdin
	}
	if st.stdout == nil {
		st.stdout = os.Stdout
	}
	if st.stderr == nil {
		st.stderr = os.Stderr
	}
	if st.lookup == nil {
		st.lookup = os.LookupEnv
	}
	return st
}

func stateFrom(ctx context.Context) *runState {
	st, _ := ctx.Value(stateKey{}).(*runState)
	return st
}

// Stdin returns the stdin of the current run, or os.Stdin outside a run.
func Stdin(ctx context.Context) io.Reader {
	if st := stateFrom(ctx); st != nil {
		return st.stdin
	}
	return os.Stdin
}

// Stdout returns the stdout of the current run, or os.Stdout outside a run.
func Stdout(ctx context.Context) io.Writer {
	if st := stateFrom(ctx); st != nil {
		return st.stdout
	}
	return os.Stdout
}

// Stderr returns the stderr of the current run, or os.Stderr outside a run.
func Stderr(ctx context.Context) io.Writer {
	if st := stateFrom(ctx); st != nil {
		return st.stderr
	}
	return os.Stderr
}

// Parent returns the arguments struct of the nearest ancestor of the selected
// command whose arguments type is T. It works in run functions, wrappers,
// checks and completion functions.
func Parent[T any](ctx context.Context) (*T, bool) {
	st := stateFrom(ctx)
	if st == nil {
		return nil, false
	}
	for i := len(st.chain) - 2; i >= 0; i-- {
		if p, ok := st.chain[i].data.(*T); ok {
			return p, true
		}
	}
	return nil, false
}

// SourceOf returns where the value of the flag or argument bound to field
// came from in the current run: cli.SourceOf(ctx, &a.Port). Fields outside
// the selected command and its ancestors are SourceUnset.
func SourceOf[T any](ctx context.Context, field *T) Source {
	st := stateFrom(ctx)
	if st == nil || field == nil {
		return SourceUnset
	}
	key := any(field)
	for _, c := range st.chain {
		for _, d := range c.flags {
			if d.ptr == key {
				return d.source
			}
		}
		for _, a := range c.args {
			if a.ptr == key {
				return a.source
			}
		}
	}
	return SourceUnset
}

var (
	errNotRoot = errors.New("cli: Run and Complete must be called on the application, not on a subcommand")
	errBusy    = errors.New("cli: the application is already running; calls must not overlap")
)

// begin guards one call: it rejects a subcommand receiver and an overlapping
// call, and returns schema errors. On success the caller must call end.
func (c *Command) begin() error {
	if c.parent != nil {
		return errNotRoot
	}
	if !c.app.busy.CompareAndSwap(false, true) {
		return errBusy
	}
	if err := c.Err(); err != nil {
		c.app.busy.Store(false)
		return err
	}
	c.resetAll()
	return nil
}

func (c *Command) end() { c.app.busy.Store(false) }

// Run runs the application with args, the words after the program name. It
// goes through these phases and stops at the first error:
//
//  1. schema errors are returned;
//  2. the command line is parsed;
//  3. if help or version was requested, it is written to stdout and Run
//     returns nil;
//  4. a selected command without a run function is an input error;
//  5. values of the selected command and its ancestors are taken from the
//     command line, the environment or the default;
//  6. required values, allowed values and checks are verified;
//  7. wrappers nest from the application down to the selected command, and
//     the innermost one calls its run function.
//
// Run prints nothing but help, version and deprecation warnings. Input
// problems are returned as *InputError and schema problems as *SchemaError;
// errors of wrappers and run functions are returned unchanged.
func (c *Command) Run(ctx context.Context, args []string, opts ...RunOption) error {
	if err := c.begin(); err != nil {
		return err
	}
	defer c.end()
	st := newState(opts)

	p := newParser(c)
	for _, w := range args {
		if err := p.word(w); err != nil {
			return err
		}
	}
	if err := p.end(); err != nil {
		return err
	}
	sel := p.cur()

	if p.help {
		fn := c.app.helpFn
		if fn == nil {
			fn = defaultHelp
		}
		return fn(st.stdout, sel.Info())
	}
	if p.version {
		_, err := fmt.Fprintf(st.stdout, "%s %s\n", c.name, c.app.version)
		return err
	}

	if sel.run == nil {
		var names []string
		for _, ch := range sel.children {
			if ch.visible() {
				names = append(names, ch.name)
			}
		}
		if len(names) == 0 {
			return inputErrorf(sel, "expected a command")
		}
		return inputErrorf(sel, "expected a command: %s", strings.Join(names, ", "))
	}

	st.chain = p.chain
	ctx = context.WithValue(ctx, stateKey{}, st)
	if err := resolve(st, p, false); err != nil {
		return err
	}
	warnDeprecated(st)
	if err := verify(ctx, st); err != nil {
		return err
	}

	next := Next(sel.run)
	for _, v := range slices.Backward(p.chain) {
		if w := v.wrap; w != nil {
			inner := next
			next = func(ctx context.Context) error { return w(ctx, inner) }
		}
	}
	return next(ctx)
}

// resolve fills the bound fields of the selected command and its ancestors.
// A source is taken whole: the command line, otherwise the environment,
// otherwise the default. In lenient mode a value that fails to convert is
// skipped instead of reported.
func resolve(st *runState, p *parser, lenient bool) error {
	sel := p.cur()
	for _, c := range st.chain {
		for _, d := range c.flags {
			if err := resolveFlag(st, d, p.vals[d]); err != nil {
				if !lenient {
					return &InputError{Path: sel.path(), Err: err}
				}
				d.reset()
			}
		}
	}
	words := p.pos
	for _, a := range sel.args {
		for len(words) > 0 {
			err := a.set(words[0])
			if err != nil {
				if !lenient {
					return inputErrorf(sel, "argument %s: invalid value %q: %w", a.name, words[0], err)
				}
				a.reset()
			} else {
				a.source = SourceCommandLine
			}
			words = words[1:]
			if !a.variadic {
				break
			}
		}
		if a.source == SourceUnset && a.hasDef {
			a.applyDefault()
			a.source = SourceDefault
		}
	}
	return nil
}

func resolveFlag(st *runState, d *flagDef, raws []string) error {
	if len(raws) > 0 {
		for _, raw := range raws {
			if err := d.set(raw); err != nil {
				return fmt.Errorf("flag --%s: invalid value %q: %w", d.name, raw, err)
			}
		}
		d.source = SourceCommandLine
		return nil
	}
	// An empty variable counts as unset, so that a variable that can only be
	// cleared, not removed, falls through to the default.
	if d.env != "" {
		if v, ok := st.lookup(d.env); ok && v != "" {
			if err := d.setEnv(v); err != nil {
				// The value is left out of the message: environment variables
				// often hold secrets, and the message ends up in logs.
				return fmt.Errorf("flag --%s: invalid value in $%s: %w", d.name, d.env, err)
			}
			d.source = SourceEnv
			return nil
		}
	}
	if d.hasDef {
		d.applyDefault()
		d.source = SourceDefault
	}
	return nil
}

func warnDeprecated(st *runState) {
	for _, c := range st.chain {
		if c.deprec != "" {
			fmt.Fprintf(st.stderr, "warning: command %q is deprecated: %s\n", c.name, c.deprec)
		}
	}
	for _, c := range st.chain {
		for _, d := range c.flags {
			if d.deprec != "" && (d.source == SourceCommandLine || d.source == SourceEnv) {
				fmt.Fprintf(st.stderr, "warning: flag --%s is deprecated: %s\n", d.name, d.deprec)
			}
		}
	}
}

// verify checks required values, allowed values and value checks of every
// node on the path, then the struct checks from the application down. Value
// checks skip unset values; only the required mark catches their absence.
func verify(ctx context.Context, st *runState) error {
	sel := st.chain[len(st.chain)-1]
	for _, c := range st.chain {
		for _, d := range c.flags {
			if d.required && d.source != SourceCommandLine && d.source != SourceEnv {
				if d.env != "" {
					return inputErrorf(sel, "missing required flag --%s (or $%s)", d.name, d.env)
				}
				return inputErrorf(sel, "missing required flag --%s", d.name)
			}
			if d.source != SourceUnset {
				if err := d.validate(); err != nil {
					return inputErrorf(sel, "flag --%s: %w", d.name, err)
				}
			}
		}
		for _, a := range c.args {
			if a.source == SourceUnset {
				if a.required() {
					return inputErrorf(sel, "missing required argument %s", a.name)
				}
				continue
			}
			if err := a.validate(); err != nil {
				return inputErrorf(sel, "argument %s: %w", a.name, err)
			}
		}
	}
	for _, c := range st.chain {
		for _, check := range c.checks {
			if err := check(ctx); err != nil {
				return &InputError{Path: sel.path(), Err: err}
			}
		}
	}
	return nil
}

// Main runs the application for func main: os.Exit(app.Main()). The first
// SIGINT or SIGTERM cancels the context; a second one terminates the program
// as usual, even if the run function does not watch the context. Main runs
// with os.Args[1:], prints an error to stderr and returns the exit code: 0 on
// success, 2 for an input error, 130 when the run ended with
// context.Canceled and 1 otherwise.
func (c *Command) Main() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop() // restore the default handling for the next signal
	}()
	return report(os.Stderr, c.Run(ctx, os.Args[1:]))
}

func report(w io.Writer, err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(w, "error: %v\n", err)
	if ie, ok := errors.AsType[*InputError](err); ok {
		fmt.Fprintf(w, "Run '%s --help' for usage.\n", strings.Join(ie.Path, " "))
		return 2
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return 1
}
