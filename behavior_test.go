package cli_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kamilov/go-cli"
)

func TestParseErrorWinsOverHelp(t *testing.T) {
	app := cli.New("tool", nil, func(context.Context, *cli.NoArgs) error { return nil })
	o, err := run(app, "--help", "--frce")
	mustInputError(t, err, "unknown flag --frce")
	mustNoOutput(t, o)
}

func TestHelpWinsOverMissingHandlerAndValues(t *testing.T) {
	type args struct{ Port int }
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Port, "port").Env("PORT")
	}, nil)
	err := app.Run(context.Background(), []string{"-h"}, envOf(map[string]string{"PORT": "bad"}), cli.WithStdout(&strings.Builder{}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestEmptyEnvCountsAsUnset(t *testing.T) {
	type args struct {
		Port int
		Name string
	}
	var got args
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Port, "port").Env("PORT").Default(8080)
		b.Flag(&a.Name, "name").Env("NAME").Required()
	}, func(_ context.Context, a *args) error { got = *a; return nil })
	ctx := context.Background()

	err := app.Run(ctx, nil, envOf(map[string]string{"PORT": "", "NAME": ""}))
	mustInputError(t, err, "missing required flag --name (or $NAME)")

	if err := app.Run(ctx, nil, envOf(map[string]string{"PORT": "", "NAME": "x"})); err != nil {
		t.Fatal(err)
	}
	if got.Port != 8080 || got.Name != "x" {
		t.Fatalf("got %+v", got)
	}
	if err := app.Run(ctx, []string{"--name", ""}, noEnv); err != nil {
		t.Fatalf("empty string from the command line is a value: %v", err)
	}
}

func TestCounterAndNegation(t *testing.T) {
	type args struct {
		Verbose int
		Color   bool
	}
	var got args
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Verbose, "verbose", cli.Count()).Short('v')
		b.Flag(&a.Color, "color", cli.Negatable()).Default(true)
	}, func(_ context.Context, a *args) error { got = *a; return nil })

	cases := []struct {
		args []string
		want args
	}{
		{nil, args{0, true}},
		{[]string{"-vvv"}, args{3, true}},
		{[]string{"--verbose", "-v"}, args{2, true}},
		{[]string{"--no-color"}, args{0, false}},
		{[]string{"--color=false"}, args{0, false}},
		{[]string{"--no-color", "--color"}, args{0, true}},
	}
	for _, tc := range cases {
		if _, err := run(app, tc.args...); err != nil {
			t.Fatal(tc.args, err)
		}
		if got != tc.want {
			t.Errorf("%v: got %+v, want %+v", tc.args, got, tc.want)
		}
	}
	_, err := run(app, "--verbose=3")
	mustInputError(t, err, "flag --verbose does not take a value")
	_, err = run(app, "--no-color=true")
	mustInputError(t, err, "flag --no-color does not take a value")
}

func TestShortFlagForms(t *testing.T) {
	type args struct {
		Force bool
		Count int
		Out   string
	}
	var got args
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Force, "force").Short('f')
		b.Flag(&a.Count, "count").Short('c')
		b.Flag(&a.Out, "out").Short('o')
	}, func(_ context.Context, a *args) error { got = *a; return nil })

	cases := []struct {
		args []string
		want args
	}{
		{[]string{"-c5"}, args{Count: 5}},
		{[]string{"-c", "5"}, args{Count: 5}},
		{[]string{"-fc5"}, args{Force: true, Count: 5}},
		{[]string{"-fo", "x"}, args{Force: true, Out: "x"}},
		{[]string{"-ofc"}, args{Out: "fc"}},
		{[]string{"-o=x"}, args{Out: "=x"}}, // only -nvalue is a short form
		{[]string{"--count=7", "--count", "8"}, args{Count: 8}},
	}
	for _, tc := range cases {
		if _, err := run(app, tc.args...); err != nil {
			t.Fatal(tc.args, err)
		}
		if got != tc.want {
			t.Errorf("%v: got %+v, want %+v", tc.args, got, tc.want)
		}
	}
	_, err := run(app, "-fx")
	mustInputError(t, err, "unknown flag -x in -fx")
}

func TestParseErrors(t *testing.T) {
	type args struct {
		Name  string
		Fetch bool
		File  string
	}
	app := cli.New[cli.NoArgs]("tool", nil, nil)
	app.Command("add", func(b *cli.Builder, a *args) {
		b.Flag(&a.Name, "name")
		b.Flag(&a.Fetch, "fetch")
		b.Arg(&a.File, "file").Optional()
	}, func(context.Context, *args) error { return nil })

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"add", "--name"}, "flag --name requires a value"},
		{[]string{"add", "a", "b"}, `unexpected argument "b"`},
		{[]string{"add", "--fetch", "false", "x"}, `unexpected argument "x"`}, // a bool flag does not take the next word
		{[]string{"--name", "x", "add"}, "unknown flag --name"},               // a child flag before the child name
		{[]string{"--", "add"}, `unexpected argument "add"`},                  // after -- no subcommands
		{[]string{"add", "--fetch=maybe"}, `flag --fetch: invalid value "maybe": invalid syntax`},
	}
	for _, tc := range cases {
		_, err := run(app, tc.args...)
		mustInputError(t, err, tc.want)
	}
}

func TestSingleDashIsPositional(t *testing.T) {
	type args struct{ File string }
	var got string
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Arg(&a.File, "file")
	}, func(_ context.Context, a *args) error { got = a.File; return nil })
	if _, err := run(app, "-"); err != nil || got != "-" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := run(app, "--", "-5"); err != nil || got != "-5" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestPositionalArguments(t *testing.T) {
	type args struct {
		Name  string
		Dir   string
		Files []string
	}
	var got args
	var src cli.Source
	handler := func(ctx context.Context, a *args) error { got, src = *a, cli.SourceOf(ctx, &a.Dir); return nil }

	required := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Arg(&a.Name, "name")
		b.Args(&a.Files, "files")
	}, handler)
	if _, err := run(required, "n", "f1", "f2"); err != nil {
		t.Fatal(err)
	}
	if got.Name != "n" || !slices.Equal(got.Files, []string{"f1", "f2"}) {
		t.Fatalf("got %+v", got)
	}
	_, err := run(required, "n")
	mustInputError(t, err, "missing required argument files")

	optional := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Arg(&a.Name, "name")
		b.Arg(&a.Dir, "dir").Default(".")
		b.Args(&a.Files, "files").Optional()
	}, handler)
	if _, err := run(optional, "n", "d", "f"); err != nil {
		t.Fatal(err)
	}
	if got.Dir != "d" || !slices.Equal(got.Files, []string{"f"}) || src != cli.SourceCommandLine {
		t.Fatalf("got %+v from %v", got, src)
	}
	if _, err := run(optional, "n"); err != nil {
		t.Fatal(err)
	}
	if got.Dir != "." || got.Files != nil || src != cli.SourceDefault {
		t.Fatalf("got %+v from %v", got, src)
	}
}

type level int8

type format string

type point struct{ X, Y int }

func TestValueTypes(t *testing.T) {
	type args struct {
		Timeout time.Duration
		Ratio   float32
		Small   uint8
		Addr    netip.Addr
		Level   level
		Format  format
		Formats []format
		Limits  map[string]int
		Point   point
	}
	var got args
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Timeout, "timeout")
		b.Flag(&a.Ratio, "ratio")
		b.Flag(&a.Small, "small")
		b.Flag(&a.Addr, "addr")
		b.Flag(&a.Level, "level").Parse(cli.FromInt)
		b.Flag(&a.Format, "format", cli.OneOf[format]("text", "json")).Parse(cli.FromString)
		b.List(&a.Formats, "formats", cli.OneOf[format]("text", "json")).Parse(cli.FromString).Env("FORMATS")
		b.Map(&a.Limits, "limit").Env("LIMITS")
		b.Flag(&a.Point, "point").Parse(func(s string) (point, error) {
			var p point
			_, err := fmt.Sscanf(s, "%d,%d", &p.X, &p.Y)
			return p, err
		})
	}, func(_ context.Context, a *args) error { got = *a; return nil })

	_, err := run(app, "--timeout", "1m30s", "--ratio", "0.5", "--small", "255", "--addr", "10.0.0.1",
		"--level", "-3", "--format", "json", "--formats", "text", "--limit", "cpu=2", "--limit", "mem=4",
		"--limit", "cpu=3", "--point", "1,2")
	if err != nil {
		t.Fatal(err)
	}
	want := args{
		Timeout: 90 * time.Second, Ratio: 0.5, Small: 255, Addr: netip.MustParseAddr("10.0.0.1"),
		Level: -3, Format: "json", Formats: []format{"text"}, Limits: map[string]int{"cpu": 3, "mem": 4},
		Point: point{1, 2},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}

	err = app.Run(context.Background(), nil, envOf(map[string]string{"FORMATS": "json,text", "LIMITS": "a=1,b=2"}))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Formats, []format{"json", "text"}) || got.Limits["a"] != 1 || got.Limits["b"] != 2 {
		t.Fatalf("from env: %+v", got)
	}

	for _, tc := range []struct{ args []string }{
		{[]string{"--small", "256"}},
		{[]string{"--level", "128"}},
	} {
		_, err := run(app, tc.args...)
		mustInputError(t, err, "value out of range")
	}
	_, err = run(app, "--small", "-1")
	mustInputError(t, err, `flag --small: invalid value "-1": invalid syntax`)
	_, err = run(app, "--formats", "xml")
	mustInputError(t, err, `flag --formats: value "xml" is not one of: text, json`)
	_, err = run(app, "--limit", "cpu")
	mustInputError(t, err, "expected key=value")
	_, err = run(app, "--addr", "nope")
	mustInputError(t, err, `flag --addr: invalid value "nope"`)
}

func TestFromIntChecksRangeOfType(t *testing.T) {
	if v, err := cli.FromInt[level]("127"); err != nil || v != 127 {
		t.Fatal(v, err)
	}
	if _, err := cli.FromInt[level]("128"); err == nil {
		t.Fatal("128 fits int8")
	}
	if _, err := cli.FromInt[uint16]("65536"); err == nil {
		t.Fatal("65536 fits uint16")
	}
	if v, err := cli.FromInt[uint64]("18446744073709551615"); err != nil || v != 1<<64-1 {
		t.Fatal(v, err)
	}
	if _, err := cli.FromInt[int]("010x"); err == nil {
		t.Fatal("invalid syntax accepted")
	}
}

func TestChecks(t *testing.T) {
	errOdd := errors.New("port must be even")
	errBoth := errors.New("--a and --b are mutually exclusive")
	type args struct{ Port, A, B int }
	var order []string
	app := cli.New("tool", func(b *cli.Builder, g *struct{}) {
		b.Check(func(context.Context) error { order = append(order, "root"); return nil })
	}, nil)
	app.Command("serve", func(b *cli.Builder, a *args) {
		b.Flag(&a.Port, "port").Env("PORT").Check(func(p int) error {
			if p%2 != 0 {
				return errOdd
			}
			return nil
		})
		b.Flag(&a.A, "a")
		b.Flag(&a.B, "b")
		b.Check(func(ctx context.Context) error {
			order = append(order, "serve")
			if cli.SourceOf(ctx, &a.A) != cli.SourceUnset && cli.SourceOf(ctx, &a.B) != cli.SourceUnset {
				return errBoth
			}
			return nil
		})
	}, func(context.Context, *args) error { return nil })

	_, err := run(app, "serve", "--port", "3")
	mustInputError(t, err, "flag --port: port must be even")
	if !errors.Is(err, errOdd) || len(order) != 0 {
		t.Fatalf("err %v, struct checks %v", err, order)
	}
	err = app.Run(context.Background(), []string{"serve"}, envOf(map[string]string{"PORT": "5"}))
	mustInputError(t, err, "port must be even")

	order = nil
	_, err = run(app, "serve", "--a", "0", "--b", "0")
	mustInputError(t, err, "mutually exclusive")
	if !errors.Is(err, errBoth) || !slices.Equal(order, []string{"root", "serve"}) {
		t.Fatalf("err %v, order %v", err, order)
	}
	order = nil
	if _, err := run(app, "serve"); err != nil || !slices.Equal(order, []string{"root", "serve"}) {
		t.Fatalf("struct checks must run with unset values: %v, %v", err, order)
	}
}

func TestSchemaErrorPointsAtDeclaration(t *testing.T) {
	type args struct{ Env, Env2 string }
	var first, second int
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		_, _, first, _ = runtime.Caller(0)
		b.Flag(&a.Env, "env")
		_, _, second, _ = runtime.Caller(0)
		b.Flag(&a.Env2, "env")
	}, nil)
	want := fmt.Sprintf("behavior_test.go:%d: flag --env already declared at behavior_test.go:%d", second+1, first+1)
	if err := app.Err(); err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	se, _ := errors.AsType[*cli.SchemaError](app.Err())
	if se.Pos != fmt.Sprintf("behavior_test.go:%d", second+1) {
		t.Fatalf("pos %q", se.Pos)
	}
}

func TestSchemaErrors(t *testing.T) {
	type unknown struct{ A int }
	type args struct {
		S     string
		N     int
		Tags  []string
		U     unknown
		Items []unknown
		Name  string
		Dir   string
		Files []string
	}
	pos := regexp.MustCompile(`^behavior_test\.go:\d+: `)
	cases := []struct {
		name  string
		build func() *cli.Command
		want  string
	}{
		{"required with default", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Flag(&a.S, "s").Required().Default("x") }, nil)
		}, "flag --s: required flag cannot have a default value"},
		{"no parser", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Flag(&a.U, "u") }, nil)
		}, "flag --u: no parser for type cli_test.unknown; set one with Parse"},
		{"slice in Flag", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Flag(&a.Tags, "tag") }, nil)
		}, "declare it with List"},
		{"no element parser", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.List(&a.Items, "item") }, nil)
		}, "flag --item: no parser for type cli_test.unknown"},
		{"default not allowed", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Flag(&a.S, "s", cli.OneOf("a", "b")).Default("c") }, nil)
		}, `flag --s: invalid default value: value "c" is not one of: a, b`},
		{"default fails check", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) {
				b.Flag(&a.N, "n").Default(-1).Check(func(n int) error {
					if n < 0 {
						return errors.New("negative")
					}
					return nil
				})
			}, nil)
		}, "flag --n: invalid default value: negative"},
		{"reserved short", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Flag(&a.S, "host").Short('h') }, nil)
		}, "short flag -h of --host conflicts with built-in short flag -h"},
		{"reserved version", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Version("1"); b.Flag(&a.S, "version") }, nil)
		}, "flag --version conflicts with built-in flag --version"},
		{"short conflict on path", func() *cli.Command {
			app := cli.New("t", func(b *cli.Builder, a *args) { b.Flag(&a.S, "server").Short('s') }, nil)
			app.Command("x", func(b *cli.Builder, a *args) { b.Flag(&a.N, "size").Short('s') }, nil)
			return app
		}, "short flag -s of --size conflicts with short flag -s of --server declared at"},
		{"argument after variadic", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Args(&a.Files, "files"); b.Arg(&a.Name, "name") }, nil)
		}, "argument name declared after variadic argument files"},
		{"required after optional", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Arg(&a.Dir, "dir").Optional(); b.Arg(&a.Name, "name") }, nil)
		}, "required argument name declared after optional argument dir"},
		{"subcommand of a node with arguments", func() *cli.Command {
			app := cli.New("t", func(b *cli.Builder, a *args) { b.Arg(&a.Name, "name") }, nil)
			app.Command[cli.NoArgs]("sub", nil, nil)
			return app
		}, `cannot add a subcommand to "t", which declares positional arguments`},
		{"two wrappers", func() *cli.Command {
			w := func(ctx context.Context, next cli.Next) error { return next(ctx) }
			return cli.New("t", func(b *cli.Builder, _ *cli.NoArgs) { b.Wrap(w); b.Wrap(w) }, nil)
		}, "wrapper already declared at"},
		{"duplicate command", func() *cli.Command {
			app := cli.New[cli.NoArgs]("t", nil, nil)
			app.Command[cli.NoArgs]("a", nil, nil)
			app.Command[cli.NoArgs]("a", nil, nil)
			return app
		}, `command "a" already declared`},
		{"alias conflict", func() *cli.Command {
			app := cli.New[cli.NoArgs]("t", nil, nil)
			app.Command[cli.NoArgs]("add", nil, nil)
			app.Command("remove", func(b *cli.Builder, _ *cli.NoArgs) { b.Alias("add") }, nil)
			return app
		}, `alias "add" conflicts with command "add"`},
		{"version on subcommand", func() *cli.Command {
			app := cli.New[cli.NoArgs]("t", nil, nil)
			app.Command("x", func(b *cli.Builder, _ *cli.NoArgs) { b.Version("1") }, nil)
			return app
		}, "only the application can declare a version"},
		{"change after bind", func() *cli.Command {
			var late *cli.Flag[string]
			app := cli.New("t", func(b *cli.Builder, a *args) { late = b.Flag(&a.S, "s") }, nil)
			late.Default("x")
			return app
		}, "declaration changed after bind returned"},
		{"empty deprecation", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Flag(&a.S, "s").Deprecated("") }, nil)
		}, "flag --s: deprecation message is empty"},
		{"invalid flag name", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Flag(&a.S, "-s") }, nil)
		}, `invalid flag name "-s"`},
		{"nil parser", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Flag(&a.S, "s").Parse(nil) }, nil)
		}, "flag --s: Parse called with a nil function"},
		{"nil list check", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.List(&a.Tags, "tag").Check(nil) }, nil)
		}, "flag --tag: Check called with a nil function"},
		{"nil argument completion", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Arg(&a.Name, "name").Complete(nil) }, nil)
		}, "argument name: Complete called with a nil function"},
		{"nil variadic parser", func() *cli.Command {
			return cli.New("t", func(b *cli.Builder, a *args) { b.Args(&a.Files, "files").Parse(nil) }, nil)
		}, "argument files: Parse called with a nil function"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := tc.build()
			err := app.Err()
			mustSchemaError(t, err, tc.want)
			if !pos.MatchString(err.Error()) {
				t.Fatalf("no declaration site in %q", err)
			}
			_, runErr := run(app, "--help")
			if runErr == nil || runErr.Error() != err.Error() {
				t.Fatalf("Run returned %v", runErr)
			}
			if _, cErr := app.Complete(context.Background(), []string{""}); cErr == nil {
				t.Fatal("Complete ignored schema errors")
			}
		})
	}
}

// A skipped declaration does not produce further errors.
func TestSchemaErrorsDoNotCascade(t *testing.T) {
	type args struct{ A, B string }
	app := cli.New[cli.NoArgs]("t", nil, nil)
	app.Command[cli.NoArgs]("x", nil, nil)
	dup := app.Command("x", func(b *cli.Builder, a *args) { b.Flag(&a.A, "a") }, nil)
	dup.Command[cli.NoArgs]("y", nil, nil)
	joined, ok := app.Err().(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 1 {
		t.Fatalf("want exactly one error, got %v", app.Err())
	}
}

func TestOverlappingCallsAreRejected(t *testing.T) {
	var app *cli.Command
	var inner, innerComplete error
	app = cli.New("tool", nil, func(ctx context.Context, _ *cli.NoArgs) error {
		inner = app.Run(ctx, nil)
		_, innerComplete = app.Complete(ctx, []string{""})
		return nil
	})
	if _, err := run(app); err != nil {
		t.Fatal(err)
	}
	if inner == nil || !strings.Contains(inner.Error(), "already running") || innerComplete == nil {
		t.Fatalf("inner run: %v, inner complete: %v", inner, innerComplete)
	}
	if _, err := run(app); err != nil {
		t.Fatalf("sequential run after overlap: %v", err)
	}
}

func TestRunOnSubcommandIsRejected(t *testing.T) {
	app := cli.New[cli.NoArgs]("tool", nil, nil)
	sub := app.Command("sub", nil, func(context.Context, *cli.NoArgs) error { return nil })
	if _, err := run(sub); err == nil {
		t.Fatal("Run on a subcommand succeeded")
	}
}

func TestValuesAreResetBetweenRuns(t *testing.T) {
	type args struct {
		Name string
		Tags []string
	}
	var got []args
	app := cli.New[cli.NoArgs]("tool", nil, nil)
	app.Command("a", func(b *cli.Builder, a *args) {
		b.Flag(&a.Name, "name")
		b.List(&a.Tags, "tag")
	}, func(_ context.Context, a *args) error { got = append(got, *a); return nil })
	app.Command("b", nil, func(context.Context, *cli.NoArgs) error { return nil })

	if _, err := run(app, "a", "--name", "x", "--tag", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(app, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(app, "a"); err != nil {
		t.Fatal(err)
	}
	if got[1].Name != "" || got[1].Tags != nil {
		t.Fatalf("second run saw %+v", got[1])
	}
}

func TestErrorKinds(t *testing.T) {
	errRun := errors.New("boom")
	app := cli.New[cli.NoArgs]("tool", nil, nil)
	app.Command("fail", nil, func(context.Context, *cli.NoArgs) error { return errRun })
	app.Command("wait", nil, func(ctx context.Context, _ *cli.NoArgs) error { <-ctx.Done(); return ctx.Err() })

	if _, err := run(app, "fail"); err != errRun {
		t.Fatalf("handler error changed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.Run(ctx, []string{"wait"}, noEnv); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamsAndParentInContext(t *testing.T) {
	type global struct{ Name string }
	app := cli.New("tool", func(b *cli.Builder, g *global) { b.Flag(&g.Name, "name") }, nil)
	app.Command("echo", nil, func(ctx context.Context, _ *cli.NoArgs) error {
		g, ok := cli.Parent[global](ctx)
		if !ok {
			return errors.New("no parent")
		}
		if _, ok := cli.Parent[time.Duration](ctx); ok {
			return errors.New("found a parent that does not exist")
		}
		buf := make([]byte, 5)
		n, _ := cli.Stdin(ctx).Read(buf)
		fmt.Fprintf(cli.Stdout(ctx), "%s %s", g.Name, buf[:n])
		fmt.Fprint(cli.Stderr(ctx), "err")
		return nil
	})
	var o output
	err := app.Run(context.Background(), []string{"echo", "--name", "hi"},
		append(o.opts(), cli.WithStdin(strings.NewReader("input")))...)
	if err != nil || o.stdout.String() != "hi input" || o.stderr.String() != "err" {
		t.Fatalf("err %v, stdout %q, stderr %q", err, o.stdout.String(), o.stderr.String())
	}
}

func TestDeprecationWarnings(t *testing.T) {
	type args struct{ Old, Older string }
	app := cli.New[cli.NoArgs]("tool", nil, nil)
	app.Command("legacy", func(b *cli.Builder, a *args) {
		b.Deprecated("use 'modern'")
		b.Flag(&a.Old, "old").Env("OLD").Deprecated("use --new")
		b.Flag(&a.Older, "older").Default("x").Deprecated("use --new")
	}, func(context.Context, *args) error { return nil })

	var o output
	err := app.Run(context.Background(), []string{"legacy"}, cli.WithStdout(&o.stdout), cli.WithStderr(&o.stderr),
		envOf(map[string]string{"OLD": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	want := "warning: command \"legacy\" is deprecated: use 'modern'\nwarning: flag --old is deprecated: use --new\n"
	if o.stderr.String() != want || o.stdout.Len() != 0 {
		t.Fatalf("stderr %q", o.stderr.String())
	}
	o2, err := run(app, "legacy", "--help")
	if err != nil || o2.stderr.Len() != 0 || !strings.Contains(o2.stdout.String(), "Deprecated: use 'modern'") {
		t.Fatalf("help: %v, stderr %q, stdout %q", err, o2.stderr.String(), o2.stdout.String())
	}
	root, _ := run(app, "--help")
	if strings.Contains(root.stdout.String(), "legacy") {
		t.Fatalf("deprecated command listed:\n%s", root.stdout.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestCustomHelpRenderer(t *testing.T) {
	var seen cli.CommandInfo
	app := cli.New("tool", func(b *cli.Builder, _ *cli.NoArgs) {
		b.HelpFunc(func(w io.Writer, cmd cli.CommandInfo) error {
			seen = cmd
			_, err := fmt.Fprintf(w, "custom %s", strings.Join(cmd.Path, "/"))
			return err
		})
	}, nil)
	app.Command("sub", func(b *cli.Builder, _ *cli.NoArgs) { b.Describe("Sub") }, nil)

	o, err := run(app, "sub", "-h")
	if err != nil || o.stdout.String() != "custom tool/sub" || seen.Description != "Sub" {
		t.Fatalf("err %v, stdout %q", err, o.stdout.String())
	}
	err = app.Run(context.Background(), []string{"--help"}, cli.WithStdout(failingWriter{}))
	if err == nil || err.Error() != "write failed" {
		t.Fatalf("write error not returned: %v", err)
	}
	plain := cli.New("tool", func(b *cli.Builder, _ *cli.NoArgs) { b.Version("1") }, nil)
	if err := plain.Run(context.Background(), []string{"--version"}, cli.WithStdout(failingWriter{})); err == nil {
		t.Fatal("version write error not returned")
	}
}

func TestInfoIsReadOnlySnapshot(t *testing.T) {
	type args struct {
		Env  string
		Tags []string
		Name string
	}
	app := cli.New("tool", func(b *cli.Builder, _ *cli.NoArgs) { b.Version("2.0") }, nil)
	app.Command("deploy", func(b *cli.Builder, a *args) {
		b.Describe("Deploy")
		b.Alias("d")
		b.Flag(&a.Env, "env", cli.OneOf("dev", "prod")).Short('e').Env("APP_ENV").Default("dev")
		b.List(&a.Tags, "tag").Hidden()
		b.Arg(&a.Name, "name").Describe("Service name")
	}, func(context.Context, *args) error { return nil })

	info := app.Info()
	if info.Version != "2.0" || len(info.Flags) != 2 || info.Flags[0].Name != "help" || info.Flags[1].Name != "version" {
		t.Fatalf("root %+v", info)
	}
	d := info.Commands[0]
	if !slices.Equal(d.Path, []string{"tool", "deploy"}) || !d.Runnable || !slices.Equal(d.Aliases, []string{"d"}) {
		t.Fatalf("deploy %+v", d)
	}
	env := d.Flags[0]
	if env.Short != 'e' || env.Env != "APP_ENV" || env.Default != "dev" || !env.HasDefault ||
		!slices.Equal(env.Choices, []string{"dev", "prod"}) || env.ValueName != "string" {
		t.Fatalf("env %+v", env)
	}
	if !d.Flags[1].Hidden || !d.Flags[1].Repeatable || len(d.InheritedFlags) != 2 {
		t.Fatalf("deploy flags %+v, inherited %+v", d.Flags, d.InheritedFlags)
	}
	if a := d.Args[0]; a.Name != "name" || !a.Required || a.Description != "Service name" {
		t.Fatalf("arg %+v", a)
	}
	d.Flags[0].Choices[0] = "changed"
	d.Aliases[0] = "changed"
	again := app.Info().Commands[0]
	if again.Flags[0].Choices[0] != "dev" || again.Aliases[0] != "d" {
		t.Fatal("snapshot shares memory with the schema")
	}
}

func TestCompletionDetails(t *testing.T) {
	type args struct {
		Output  string
		Tags    []string
		Secret  string
		Old     string
		Verbose bool
		File    string
		Rest    []string
	}
	errComplete := errors.New("lookup failed")
	app := cli.New[cli.NoArgs]("tool", nil, nil)
	app.Command("run", func(b *cli.Builder, a *args) {
		b.Flag(&a.Output, "output").Short('o')
		b.List(&a.Tags, "tag")
		b.Flag(&a.Secret, "secret").Hidden()
		b.Flag(&a.Old, "old").Deprecated("gone")
		b.Flag(&a.Verbose, "verbose").Short('v')
		b.Arg(&a.File, "file").Complete(func(_ context.Context, p string) ([]cli.Candidate, error) {
			return []cli.Candidate{{Value: p + "-file"}}, nil
		})
		b.Args(&a.Rest, "rest").Optional().Complete(func(context.Context, string) ([]cli.Candidate, error) {
			return nil, errComplete
		})
	}, func(context.Context, *args) error { return nil })

	c := complete(t, app, "run", "--output", "x", "--tag", "a", "--")
	want := []string{"--tag", "--verbose", "--help"}
	if c.Kind != cli.CompleteFlagName || !slices.Equal(values(c), want) {
		t.Fatalf("flag names: %+v", c)
	}
	c = complete(t, app, "run", "-")
	if !slices.Contains(values(c), "-o") || !slices.Contains(values(c), "-h") || slices.Contains(values(c), "--secret") {
		t.Fatalf("dash: %v", values(c))
	}
	c = complete(t, app, "run", "-vo")
	if c.Kind != cli.CompleteFlagName || !slices.Equal(values(c), []string{"-vo"}) {
		t.Fatalf("-vo: %+v", c)
	}
	c = complete(t, app, "run", "f")
	if c.Kind != cli.CompleteArg || c.Name != "file" || !slices.Equal(values(c), []string{"f-file"}) {
		t.Fatalf("arg: %+v", c)
	}
	c = complete(t, app, "run", "--", "-x")
	if c.Kind != cli.CompleteArg || c.Name != "file" || c.Prefix != "-x" {
		t.Fatalf("after --: %+v", c)
	}
	if _, err := app.Complete(context.Background(), []string{"run", "a", "b"}); err != errComplete {
		t.Fatalf("completion function error: %v", err)
	}
	c = complete(t, app)
	if c.Kind != cli.CompleteCommand || !slices.Equal(values(c), []string{"run"}) {
		t.Fatalf("no words: %+v", c)
	}
	c = complete(t, app, "run", "--verbose=")
	if c.Kind != cli.CompleteNone {
		t.Fatalf("bool value: %+v", c)
	}
}

func TestCompletionResolvesEnvAndDefaults(t *testing.T) {
	type args struct{ Region, Name string }
	var seen []string
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Region, "region").Env("REGION")
		b.Flag(&a.Name, "name").Default("n").Complete(func(ctx context.Context, _ string) ([]cli.Candidate, error) {
			seen = append(seen, a.Region, fmt.Sprint(cli.SourceOf(ctx, &a.Region) == cli.SourceEnv),
				fmt.Sprint(cli.SourceOf(ctx, &a.Name) == cli.SourceDefault))
			return nil, nil
		})
	}, nil)
	_, err := app.Complete(context.Background(), []string{"--name", ""}, envOf(map[string]string{"REGION": "eu"}))
	if err != nil || !slices.Equal(seen, []string{"eu", "true", "true"}) {
		t.Fatalf("seen %v, %v", seen, err)
	}
	seen = nil
	_, err = app.Complete(context.Background(), []string{"--region", "us", "--name", ""}, noEnv)
	if err != nil || !slices.Equal(seen, []string{"us", "false", "true"}) {
		t.Fatalf("seen %v, %v", seen, err)
	}
}

func TestEnvValueIsNotEchoedInErrors(t *testing.T) {
	type args struct{ Port int }
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Port, "port").Env("DSN")
	}, func(context.Context, *args) error { return nil })
	err := app.Run(context.Background(), nil, envOf(map[string]string{"DSN": "postgres://u:secret@h/db"}))
	ie := mustInputError(t, err, "flag --port: invalid value in $DSN: invalid syntax")
	if strings.Contains(ie.Error(), "secret") {
		t.Fatalf("environment value leaked: %q", ie.Error())
	}
}

func TestSuggestions(t *testing.T) {
	type args struct {
		Force, Older, Secret, V string
	}
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Force, "force")
		b.Flag(&a.Older, "older").Deprecated("use --new")
		b.Flag(&a.Secret, "secret").Hidden()
		b.Flag(&a.V, "v")
	}, func(context.Context, *args) error { return nil })
	app.Command("deploy", nil, func(context.Context, *cli.NoArgs) error { return nil })

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--forc"}, "unknown flag --forc (did you mean --force?)"},
		{[]string{"--olde"}, "unknown flag --olde"},   // deprecated flags are not suggested
		{[]string{"--secre"}, "unknown flag --secre"}, // nor hidden ones
		{[]string{"--q"}, "unknown flag --q"},         // one character is too short to guess
		{[]string{"--="}, "unknown flag --"},
	}
	for _, tc := range cases {
		_, err := run(app, tc.args...)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%v: got %v, want %q", tc.args, err, tc.want)
		}
	}
}
