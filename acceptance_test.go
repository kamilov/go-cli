package cli_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kamilov/go-cli"
)

// envOf returns a lookup over m.
func envOf(m map[string]string) cli.RunOption {
	return cli.WithEnv(func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	})
}

// noEnv isolates a run from the real environment.
var noEnv = envOf(nil)

type output struct{ stdout, stderr bytes.Buffer }

func (o *output) opts() []cli.RunOption {
	return []cli.RunOption{cli.WithStdout(&o.stdout), cli.WithStderr(&o.stderr), noEnv}
}

func run(app *cli.Command, args ...string) (*output, error) {
	var o output
	err := app.Run(context.Background(), args, o.opts()...)
	return &o, err
}

func mustInputError(t *testing.T, err error, contains string) *cli.InputError {
	t.Helper()
	ie, ok := errors.AsType[*cli.InputError](err)
	if !ok {
		t.Fatalf("want *InputError containing %q, got %T: %v", contains, err, err)
	}
	if !strings.Contains(ie.Error(), contains) {
		t.Fatalf("want error containing %q, got %q", contains, ie.Error())
	}
	if strings.Contains(ie.Error(), "\n") {
		t.Fatalf("input error must be one line, got %q", ie.Error())
	}
	return ie
}

func mustSchemaError(t *testing.T, err error, contains string) {
	t.Helper()
	if _, ok := errors.AsType[*cli.SchemaError](err); !ok {
		t.Fatalf("want *SchemaError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("want schema error containing %q, got %q", contains, err.Error())
	}
}

func mustNoOutput(t *testing.T, o *output) {
	t.Helper()
	if o.stdout.Len() != 0 || o.stderr.Len() != 0 {
		t.Fatalf("library must print nothing, got stdout %q, stderr %q", o.stdout.String(), o.stderr.String())
	}
}

// A-1: an int8 field rejects 300 instead of wrapping it.
func TestIntegerOutOfRangeIsInputError(t *testing.T) {
	type args struct{ Level int8 }
	called := false
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Level, "level")
	}, func(context.Context, *args) error { called = true; return nil })

	_, err := run(app, "--level", "300")
	mustInputError(t, err, "value out of range")
	if called {
		t.Fatal("handler was called")
	}
}

// A-2: after "--" nothing is a flag.
func TestWordsAfterDoubleDashArePositional(t *testing.T) {
	type args struct {
		A, B  bool
		Words []string
	}
	var got args
	app := cli.New[cli.NoArgs]("tool", nil, nil)
	app.Command("run", func(b *cli.Builder, a *args) {
		b.Flag(&a.A, "all").Short('a')
		b.Flag(&a.B, "brief").Short('b')
		b.Args(&a.Words, "words").Optional()
	}, func(_ context.Context, a *args) error { got = *a; return nil })

	if _, err := run(app, "run", "--", "-ab"); err != nil {
		t.Fatal(err)
	}
	if got.A || got.B || !slices.Equal(got.Words, []string{"-ab"}) {
		t.Fatalf("got %+v", got)
	}
}

// A-3: a flag that takes a value takes the next word even if it starts with a dash.
func TestValueFlagTakesNextWordStartingWithDash(t *testing.T) {
	type args struct {
		A, B    bool
		Message string
	}
	var got args
	app := cli.New[cli.NoArgs]("tool", nil, nil)
	app.Command("run", func(b *cli.Builder, a *args) {
		b.Flag(&a.A, "all").Short('a')
		b.Flag(&a.B, "brief").Short('b')
		b.Flag(&a.Message, "message")
	}, func(_ context.Context, a *args) error { got = *a; return nil })

	if _, err := run(app, "run", "--message", "-ab"); err != nil {
		t.Fatal(err)
	}
	if got.Message != "-ab" || got.A || got.B {
		t.Fatalf("got %+v", got)
	}
}

// A-4: the environment is read only when the command line has no value.
func TestCommandLineOverridesInvalidEnv(t *testing.T) {
	type args struct{ Port int }
	var got args
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Port, "port").Env("PORT")
	}, func(_ context.Context, a *args) error { got = *a; return nil })
	env := envOf(map[string]string{"PORT": "abc"})

	if err := app.Run(context.Background(), []string{"--port", "80"}, env); err != nil {
		t.Fatal(err)
	}
	if got.Port != 80 {
		t.Fatalf("port = %d", got.Port)
	}
	err := app.Run(context.Background(), nil, env)
	mustInputError(t, err, "$PORT")
}

// A-5: help needs neither required values nor the handler.
func TestHelpIgnoresRequiredFlags(t *testing.T) {
	type args struct{ Env string }
	var calls []string
	app := cli.New("tool", func(b *cli.Builder, _ *cli.NoArgs) {
		b.Wrap(func(ctx context.Context, next cli.Next) error { calls = append(calls, "wrap"); return next(ctx) })
	}, nil)
	app.Command("deploy", func(b *cli.Builder, a *args) {
		b.Describe("Deploy the service")
		b.Flag(&a.Env, "env").Required()
	}, func(context.Context, *args) error { calls = append(calls, "run"); return nil })

	o, err := run(app, "deploy", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(o.stdout.String(), "Deploy the service") || !strings.Contains(o.stdout.String(), "--env") {
		t.Fatalf("help not in stdout: %q", o.stdout.String())
	}
	if len(calls) != 0 {
		t.Fatalf("called %v", calls)
	}
}

// A-6: a run that changes a list and a map does not change the defaults.
func TestRunsDoNotShareValues(t *testing.T) {
	type args struct {
		Tags   []string
		Labels map[string]string
	}
	var got []args
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.List(&a.Tags, "tag").Default("base")
		b.Map(&a.Labels, "label").Default(map[string]string{"k": "v"})
	}, func(_ context.Context, a *args) error {
		got = append(got, args{slices.Clone(a.Tags), map[string]string{"k": a.Labels["k"]}})
		a.Tags[0] = "changed"
		a.Labels["k"] = "changed"
		a.Labels["extra"] = "x"
		return nil
	})

	if _, err := run(app, "--tag", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(app); err != nil {
		t.Fatal(err)
	}
	if _, err := run(app); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[0].Tags, []string{"a"}) {
		t.Fatalf("first run tags = %v", got[0].Tags)
	}
	for _, g := range got[1:] {
		if !slices.Equal(g.Tags, []string{"base"}) || g.Labels["k"] != "v" {
			t.Fatalf("defaults changed: %+v", g)
		}
	}
}

// A-7: a parse error carries a suggestion, and nothing runs or prints.
func TestUnknownFlagSuggestsClosest(t *testing.T) {
	type args struct{ Force bool }
	var calls []string
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Force, "force")
		b.Wrap(func(ctx context.Context, next cli.Next) error { calls = append(calls, "wrap"); return next(ctx) })
	}, func(context.Context, *args) error { calls = append(calls, "run"); return nil })

	o, err := run(app, "--frce")
	mustInputError(t, err, "did you mean --force?")
	mustNoOutput(t, o)
	if len(calls) != 0 {
		t.Fatalf("called %v", calls)
	}
}

// A-8 and the rule it illustrates: one source wins as a whole.
func TestSourceIsTakenWhole(t *testing.T) {
	type args struct{ Tags []string }
	var got []string
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.List(&a.Tags, "tag").Env("TAGS").Default("base")
	}, func(_ context.Context, a *args) error { got = a.Tags; return nil })
	ctx := context.Background()
	env := envOf(map[string]string{"TAGS": "a,b"})

	cases := []struct {
		args []string
		opt  cli.RunOption
		want []string
	}{
		{[]string{"--tag", "c"}, env, []string{"c"}},
		{[]string{"--tag", "c", "--tag", "d"}, env, []string{"c", "d"}},
		{nil, env, []string{"a", "b"}},
		{nil, noEnv, []string{"base"}},
	}
	for _, tc := range cases {
		if err := app.Run(ctx, tc.args, tc.opt); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%v: got %v, want %v", tc.args, got, tc.want)
		}
	}
}

// A-9: a wrapper failing before next stops the run; outer wrappers release.
func TestWrapperErrorReleasesOuterResources(t *testing.T) {
	errPrepare := errors.New("prepare failed")
	var log []string
	app := cli.New("tool", func(b *cli.Builder, _ *cli.NoArgs) {
		b.Wrap(func(ctx context.Context, next cli.Next) error {
			log = append(log, "open root")
			defer func() { log = append(log, "close root") }()
			return next(ctx)
		})
	}, nil)
	app.Command("sub", func(b *cli.Builder, _ *cli.NoArgs) {
		b.Wrap(func(context.Context, cli.Next) error { return errPrepare })
	}, func(context.Context, *cli.NoArgs) error { log = append(log, "run"); return nil })

	_, err := run(app, "sub")
	if err != errPrepare {
		t.Fatalf("err = %v, want the wrapper error unchanged", err)
	}
	if !slices.Equal(log, []string{"open root", "close root"}) {
		t.Fatalf("log = %v", log)
	}
}

// A-10: a wrapper may join the handler error with its own release error.
func TestWrapperJoinsHandlerAndReleaseErrors(t *testing.T) {
	e1, e2 := errors.New("E1"), errors.New("E2")
	app := cli.New("tool", func(b *cli.Builder, _ *cli.NoArgs) {
		b.Wrap(func(ctx context.Context, next cli.Next) (err error) {
			defer func() { err = errors.Join(err, e2) }()
			return next(ctx)
		})
	}, func(context.Context, *cli.NoArgs) error { return e1 })

	_, err := run(app)
	if !errors.Is(err, e1) || !errors.Is(err, e2) {
		t.Fatalf("err = %v", err)
	}
}

// A-11: a flag name repeats on a path; siblings may share it.
func TestFlagNameConflictOnPath(t *testing.T) {
	type envArgs struct{ Env string }
	bindEnv := func(b *cli.Builder, a *envArgs) { b.Flag(&a.Env, "env") }
	noop := func(context.Context, *envArgs) error { return nil }

	siblings := cli.New[cli.NoArgs]("tool", nil, nil)
	siblings.Command("deploy", bindEnv, noop)
	siblings.Command("status", bindEnv, noop)
	if err := siblings.Err(); err != nil {
		t.Fatalf("siblings: %v", err)
	}

	app := cli.New("tool", bindEnv, nil)
	app.Command("deploy", bindEnv, noop)
	mustSchemaError(t, app.Err(), "flag --env already declared at")
	_, err := run(app, "deploy", "--help")
	mustSchemaError(t, err, "flag --env already declared at")
}

// A-12: a generated negation takes part in name conflicts.
func TestNegationConflictsWithDeclaredFlag(t *testing.T) {
	type args struct{ Color, NoColor bool }
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Color, "color", cli.Negatable())
		b.Flag(&a.NoColor, "no-color")
	}, nil)
	mustSchemaError(t, app.Err(), "flag --no-color conflicts with flag --no-color (negation of --color)")
}

// A-13: flags of an ancestor are accepted before and after subcommand names.
func TestParentFlagBeforeAndAfterSubcommand(t *testing.T) {
	type global struct{ Output string }
	var got []string
	app := cli.New("tool", func(b *cli.Builder, g *global) {
		b.Flag(&g.Output, "output").Default("text")
	}, nil)
	remote := app.Command[cli.NoArgs]("remote", nil, nil)
	remote.Command("add", nil, func(ctx context.Context, _ *cli.NoArgs) error {
		g, _ := cli.Parent[global](ctx)
		got = append(got, g.Output)
		return nil
	})

	for _, args := range [][]string{
		{"--output", "json", "remote", "add"},
		{"remote", "add", "--output", "json"},
		{"remote", "--output=json", "add"},
	} {
		if _, err := run(app, args...); err != nil {
			t.Fatal(args, err)
		}
	}
	if !slices.Equal(got, []string{"json", "json", "json"}) {
		t.Fatalf("got %v", got)
	}
}

// A-14: a misspelt subcommand is an input error with a suggestion.
func TestUnknownSubcommandSuggests(t *testing.T) {
	app := cli.New[cli.NoArgs]("tool", nil, nil)
	app.Command("deploy", nil, func(context.Context, *cli.NoArgs) error { return nil })

	o, err := run(app, "deplyo")
	ie := mustInputError(t, err, `unknown command "deplyo" (did you mean "deploy"?)`)
	if !slices.Equal(ie.Path, []string{"tool"}) {
		t.Fatalf("path = %v", ie.Path)
	}
	mustNoOutput(t, o)
}

// A-15: an explicit zero counts as set.
func TestExplicitZeroIsSet(t *testing.T) {
	type args struct{ Count int }
	var got int
	var src cli.Source
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Count, "count").Default(5)
	}, func(ctx context.Context, a *args) error {
		got, src = a.Count, cli.SourceOf(ctx, &a.Count)
		return nil
	})

	if _, err := run(app, "--count", "0"); err != nil {
		t.Fatal(err)
	}
	if got != 0 || src != cli.SourceCommandLine {
		t.Fatalf("got %d from %v", got, src)
	}
	if _, err := run(app); err != nil {
		t.Fatal(err)
	}
	if got != 5 || src != cli.SourceDefault {
		t.Fatalf("got %d from %v", got, src)
	}
}

// A-16: a positional argument is required unless declared otherwise.
func TestMissingRequiredArgument(t *testing.T) {
	type args struct{ Name string }
	var calls []string
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Arg(&a.Name, "name")
		b.Wrap(func(ctx context.Context, next cli.Next) error { calls = append(calls, "wrap"); return next(ctx) })
	}, func(context.Context, *args) error { calls = append(calls, "run"); return nil })

	_, err := run(app)
	mustInputError(t, err, "missing required argument name")
	if len(calls) != 0 {
		t.Fatalf("called %v", calls)
	}
}

type regionArgs struct{ Region string }

type deployArgs struct{ Env, Profile string }

// completionApp has a global --region, a deploy command with a required
// --env, a --profile completed from region and env, a hidden debug command
// and an --output flag with allowed values.
func completionApp(seen *[]string, calls *[]string) *cli.Command {
	app := cli.New("tool", func(b *cli.Builder, a *regionArgs) {
		b.Flag(&a.Region, "region")
		b.Wrap(func(ctx context.Context, next cli.Next) error { *calls = append(*calls, "wrap"); return next(ctx) })
	}, nil)
	app.Command("deploy", func(b *cli.Builder, a *deployArgs) {
		b.Flag(&a.Env, "env").Required()
		b.Flag(&a.Profile, "profile").Complete(func(ctx context.Context, prefix string) ([]cli.Candidate, error) {
			r, _ := cli.Parent[regionArgs](ctx)
			*seen = append(*seen, r.Region, a.Env, prefix)
			return []cli.Candidate{{Value: r.Region + "-" + a.Env + "-admin"}}, nil
		})
	}, func(context.Context, *deployArgs) error { *calls = append(*calls, "run"); return nil })
	app.Command("debug", func(b *cli.Builder, _ *cli.NoArgs) { b.Hidden() },
		func(context.Context, *cli.NoArgs) error { return nil })
	return app
}

func complete(t *testing.T, app *cli.Command, words ...string) cli.Completion {
	t.Helper()
	c, err := app.Complete(context.Background(), words, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func values(c cli.Completion) []string {
	var v []string
	for _, cand := range c.Candidates {
		v = append(v, cand.Value)
	}
	return v
}

// A-17: a completion function sees entered values of its command and ancestors.
func TestCompletionSeesEnteredValues(t *testing.T) {
	var seen, calls []string
	app := completionApp(&seen, &calls)

	c := complete(t, app, "--region", "eu", "deploy", "--env", "prod", "--profile", "a")
	if c.Kind != cli.CompleteFlagValue || c.Name != "profile" || c.Prefix != "a" {
		t.Fatalf("got %+v", c)
	}
	if !slices.Equal(seen, []string{"eu", "prod", "a"}) {
		t.Fatalf("function saw %v", seen)
	}
	if !slices.Equal(values(c), []string{"eu-prod-admin"}) {
		t.Fatalf("candidates %v", values(c))
	}
	if len(calls) != 0 {
		t.Fatalf("called %v", calls)
	}
}

// A-18: completion does not check required values and runs nothing.
func TestCompletionSkipsRequiredChecks(t *testing.T) {
	var seen, calls []string
	app := completionApp(&seen, &calls)

	c := complete(t, app, "deploy", "--pro")
	if c.Kind != cli.CompleteFlagName || !slices.Equal(values(c), []string{"--profile"}) {
		t.Fatalf("got %+v", c)
	}
	if len(calls) != 0 {
		t.Fatalf("called %v", calls)
	}
}

// A-19: hidden commands are not offered.
func TestCompletionHidesHiddenCommands(t *testing.T) {
	var seen, calls []string
	app := completionApp(&seen, &calls)

	c := complete(t, app, "de")
	if c.Kind != cli.CompleteCommand || !slices.Equal(values(c), []string{"deploy"}) {
		t.Fatalf("got %+v", c)
	}
}

func outputApp() *cli.Command {
	type args struct {
		Output  string
		Verbose bool
	}
	return cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Output, "output", cli.OneOf("text", "json")).Short('o')
		b.Flag(&a.Verbose, "verbose").Short('v')
	}, func(context.Context, *args) error { return nil })
}

// A-20: allowed values are offered when there is no completion function.
func TestCompletionOffersAllowedValues(t *testing.T) {
	c := complete(t, outputApp(), "--output=j")
	if c.Kind != cli.CompleteFlagValue || !slices.Equal(values(c), []string{"json"}) {
		t.Fatalf("got %+v", c)
	}
}

// A-21: an unknown word before the last one gives an empty result.
func TestCompletionAfterUnknownCommandIsEmpty(t *testing.T) {
	var seen, calls []string
	app := completionApp(&seen, &calls)

	c, err := app.Complete(context.Background(), []string{"deplyo", "--pro"}, noEnv)
	if err != nil || c.Kind != cli.CompleteNone || len(c.Candidates) != 0 {
		t.Fatalf("got %+v, %v", c, err)
	}
}

// A-22: a node without a handler shows help and version, and asks for a
// command otherwise.
func TestHelpAndVersionOnNodesWithoutHandler(t *testing.T) {
	var calls []string
	app := cli.New("tool", func(b *cli.Builder, _ *cli.NoArgs) {
		b.Version("1.2.3")
		b.Wrap(func(ctx context.Context, next cli.Next) error { calls = append(calls, "wrap"); return next(ctx) })
	}, nil)
	remote := app.Command[cli.NoArgs]("remote", nil, nil)
	remote.Command("add", nil, func(context.Context, *cli.NoArgs) error { return nil })

	for _, args := range [][]string{{"--help"}, {"remote", "--help"}, {"--version"}} {
		o, err := run(app, args...)
		if err != nil || o.stdout.Len() == 0 {
			t.Fatalf("%v: err %v, stdout %q", args, err, o.stdout.String())
		}
	}
	o, _ := run(app, "--version")
	if o.stdout.String() != "tool 1.2.3\n" {
		t.Fatalf("version output %q", o.stdout.String())
	}
	_, err := run(app, "remote")
	ie := mustInputError(t, err, "expected a command")
	if !slices.Equal(ie.Path, []string{"tool", "remote"}) {
		t.Fatalf("path = %v", ie.Path)
	}
	if len(calls) != 0 {
		t.Fatalf("called %v", calls)
	}
}

// A-23: the last word is read with the grammar of Run, including attached
// values and groups of short flags.
func TestCompletionReadsAttachedShortValue(t *testing.T) {
	app := outputApp()
	for _, words := range [][]string{{"--output=j"}, {"-o", "j"}, {"-oj"}, {"-voj"}} {
		c := complete(t, app, words...)
		if c.Kind != cli.CompleteFlagValue || c.Name != "output" || c.Prefix != "j" ||
			!slices.Equal(values(c), []string{"json"}) {
			t.Errorf("%v: got %+v", words, c)
		}
	}
}

// A-24: an optional flag without a source stays unset and is not checked.
func TestOptionalFlagWithoutSourceIsUnset(t *testing.T) {
	type args struct{ Env string }
	var got string
	src := cli.SourceDefault
	app := cli.New("tool", func(b *cli.Builder, a *args) {
		b.Flag(&a.Env, "env", cli.OneOf("dev", "prod")).Env("APP_ENV")
	}, func(ctx context.Context, a *args) error {
		got, src = a.Env, cli.SourceOf(ctx, &a.Env)
		return nil
	})

	if _, err := run(app); err != nil {
		t.Fatal(err)
	}
	if got != "" || src != cli.SourceUnset {
		t.Fatalf("got %q from %v", got, src)
	}
}
