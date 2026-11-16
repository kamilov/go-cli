// Package cli builds command-line applications from typed declarations.
//
// A command is a pair of a bind function, which declares flags and positional
// arguments by pointing at fields of an arguments struct, and a run function,
// which receives the filled struct:
//
//	app := cli.New("tool", func(b *cli.Builder, a *Args) {
//		b.Flag(&a.Env, "env", cli.OneOf("dev", "prod")).Short('e').Default("dev")
//	}, func(ctx context.Context, a *Args) error {
//		fmt.Fprintln(cli.Stdout(ctx), a.Env)
//		return nil
//	})
//	os.Exit(app.Main())
//
// Bind runs once, when the command is registered, so the whole schema is known
// before any command line is parsed. Declaration mistakes are collected as
// schema errors and returned by Run, Complete and Err; the library never panics
// on them.
//
// Each node owns a single arguments struct for the lifetime of the application.
// Bound fields are reset before every Run and Complete, so calls on one
// application must not overlap; an overlapping call returns an error.
package cli
