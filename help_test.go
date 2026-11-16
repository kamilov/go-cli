package cli_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kamilov/go-cli"
)

// sketchApp is the usage sketch from the design plan, with descriptions.
func sketchApp() *cli.Command {
	type globalArgs struct {
		Output  string
		Verbose int
	}
	type remoteArgs struct{ Timeout time.Duration }
	type addArgs struct {
		Name, URL string
		Fetch     bool
		Tags      []string
	}
	app := cli.New("git", func(b *cli.Builder, g *globalArgs) {
		b.Describe("Demo tool")
		b.Version("1.0.0")
		b.Flag(&g.Output, "output", cli.OneOf("text", "json")).Short('o').Default("text").Describe("Output format")
		b.Flag(&g.Verbose, "verbose", cli.Count()).Short('v').Describe("Increase verbosity")
	}, nil)
	remote := app.Command("remote", func(b *cli.Builder, r *remoteArgs) {
		b.Describe("Manage remotes")
		b.Alias("rmt")
		b.Flag(&r.Timeout, "timeout").Env("GIT_REMOTE_TIMEOUT").Default(30 * time.Second).Describe("Network timeout")
		b.Wrap(func(ctx context.Context, next cli.Next) error {
			ctx, cancel := context.WithTimeout(ctx, r.Timeout)
			defer cancel()
			return next(ctx)
		})
	}, nil)
	remote.Command("add", func(b *cli.Builder, a *addArgs) {
		b.Describe("Add a remote")
		b.Flag(&a.Fetch, "fetch").Short('f').Describe("Fetch after adding")
		b.List(&a.Tags, "tag").Describe("Tag to attach")
		b.Arg(&a.Name, "name").Describe("Remote name")
		b.Arg(&a.URL, "url").Describe("Remote URL")
	}, func(context.Context, *addArgs) error { return nil })
	return app
}

func TestDefaultHelpLayout(t *testing.T) {
	o, err := run(sketchApp(), "rmt", "add", "--help")
	if err != nil {
		t.Fatal(err)
	}
	want := `Add a remote

Usage:
  git remote add [flags] <name> <url>

Arguments:
  name  Remote name
  url   Remote URL

Flags:
  -f, --fetch       Fetch after adding
      --tag string  Tag to attach (repeatable)

Inherited flags:
  -o, --output string     Output format (one of: text, json; default: text)
  -v, --verbose           Increase verbosity (repeatable)
  -h, --help              Show help
      --version           Show version
      --timeout duration  Network timeout (default: 30s; env: GIT_REMOTE_TIMEOUT)
`
	if got := o.stdout.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	o, err = run(sketchApp(), "remote", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"git remote <command> [flags]", "Aliases:\n  rmt", "Commands:\n  add  Add a remote",
		"Run 'git remote <command> --help'"} {
		if !strings.Contains(o.stdout.String(), part) {
			t.Errorf("missing %q in:\n%s", part, o.stdout.String())
		}
	}
}

func TestHiddenItemsAreNotInHelp(t *testing.T) {
	type args struct{ Secret string }
	app := cli.New("tool", func(b *cli.Builder, a *args) { b.Flag(&a.Secret, "secret").Hidden() }, nil)
	app.Command("debug", func(b *cli.Builder, _ *cli.NoArgs) { b.Hidden() }, nil)
	app.Command("deploy", nil, func(context.Context, *cli.NoArgs) error { return nil })
	o, err := run(app, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if s := o.stdout.String(); strings.Contains(s, "secret") || strings.Contains(s, "debug") || !strings.Contains(s, "deploy") {
		t.Fatalf("help:\n%s", s)
	}
}

// TestMainExitCodes runs the test binary as a child process that calls Main,
// because Main reads os.Args and writes to os.Stderr.
func TestMainExitCodes(t *testing.T) {
	if args, ok := os.LookupEnv("CLI_TEST_MAIN_ARGS"); ok {
		app := cli.New[cli.NoArgs]("tool", nil, nil)
		app.Command("ok", nil, func(context.Context, *cli.NoArgs) error { return nil })
		app.Command("fail", nil, func(context.Context, *cli.NoArgs) error { return errors.New("boom") })
		app.Command("cancel", nil, func(context.Context, *cli.NoArgs) error { return context.Canceled })
		if strings.Contains(args, "schema") {
			app.Command("ok", nil, func(context.Context, *cli.NoArgs) error { return nil })
		}
		os.Args = append([]string{"tool"}, strings.Fields(args)...)
		os.Exit(app.Main())
	}

	cases := []struct {
		args   string
		code   int
		stderr string
	}{
		{"ok", 0, ""},
		{"fail", 1, "error: boom\n"},
		{"oops", 2, "error: unknown command \"oops\"\nRun 'tool --help' for usage.\n"},
		{"cancel", 130, "error: context canceled\n"},
		{"schema ok", 1, "already declared"},
	}
	for _, tc := range cases {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMainExitCodes$")
		cmd.Env = append(os.Environ(), "CLI_TEST_MAIN_ARGS="+tc.args)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != tc.code || !strings.Contains(stderr.String(), tc.stderr) {
			t.Errorf("%q: code %d, stderr %q; want %d, %q", tc.args, code, stderr.String(), tc.code, tc.stderr)
		}
	}
}

// TestMainSecondSignalTerminates runs a child whose run function ignores the
// context: the first SIGINT only cancels the context, a later one must end
// the process.
func TestMainSecondSignalTerminates(t *testing.T) {
	if os.Getenv("CLI_TEST_MAIN_SIGNAL") != "" {
		app := cli.New("tool", nil, func(ctx context.Context, _ *cli.NoArgs) error {
			fmt.Println("started")
			<-ctx.Done()
			time.Sleep(time.Minute)
			return nil
		})
		os.Args = []string{"tool"}
		os.Exit(app.Main())
	}
	if runtime.GOOS == "windows" {
		t.Skip("sending SIGINT to a child process is not supported on Windows")
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestMainSecondSignalTerminates$")
	cmd.Env = append(os.Environ(), "CLI_TEST_MAIN_SIGNAL=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if line, _ := bufio.NewReader(stdout).ReadString('\n'); line != "started\n" {
		t.Fatalf("child output %q", line)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// Keep signalling: the first signal is consumed by the cancellation, and
	// the moment the default handling is restored is not observable.
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("child exited successfully instead of being terminated")
			}
			return
		case <-tick.C:
			_ = cmd.Process.Signal(os.Interrupt)
		case <-timeout:
			_ = cmd.Process.Kill()
			t.Fatal("child survived repeated SIGINT")
		}
	}
}
