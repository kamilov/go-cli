package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// CommandInfo is a read-only snapshot of a command and its subtree, used by
// help renderers and documentation generators. It holds copies, not pointers
// to run values.
type CommandInfo struct {
	Name string
	// Path is the command path from the application, including Name.
	Path        []string
	Aliases     []string
	Description string
	// Version is the application version; set for the application only.
	Version string
	Hidden  bool
	// Deprecated is the deprecation message; empty if not deprecated.
	Deprecated string
	// Runnable reports whether the command has a run function.
	Runnable bool
	// Flags are the command's own flags. For the application they include
	// the built-in --help and, if a version is declared, --version.
	Flags []FlagInfo
	// InheritedFlags are the flags of the ancestors, from the application
	// down; they also apply to this command.
	InheritedFlags []FlagInfo
	Args           []ArgInfo
	Commands       []CommandInfo
}

// FlagInfo describes a flag. Values are formatted with fmt.Sprint; list
// elements are joined with commas.
type FlagInfo struct {
	Name  string
	Short rune // 0 if none
	Env   string
	// Description is the help text.
	Description string
	// ValueName is the value placeholder, such as "string", "int",
	// "duration", "key=string" or "value"; empty if the flag takes no value.
	ValueName  string
	Repeatable bool // list, map or counter
	Negatable  bool
	Default    string
	HasDefault bool
	Choices    []string
	Required   bool
	Hidden     bool
	// Deprecated is the deprecation message; empty if not deprecated.
	Deprecated string
}

// ArgInfo describes a positional argument.
type ArgInfo struct {
	Name        string
	Description string
	Default     string
	HasDefault  bool
	Required    bool
	Variadic    bool // the trailing list
}

// Info returns a snapshot of the command and its subtree.
func (c *Command) Info() CommandInfo {
	ci := CommandInfo{
		Name:        c.name,
		Path:        c.path(),
		Aliases:     slices.Clone(c.aliases),
		Description: c.desc,
		Hidden:      c.hidden,
		Deprecated:  c.deprec,
		Runnable:    c.run != nil,
		Flags:       c.flagInfos(),
	}
	if c.parent == nil {
		ci.Version = c.app.version
	}
	var anc []*Command
	for a := c.parent; a != nil; a = a.parent {
		anc = append(anc, a)
	}
	for i := len(anc) - 1; i >= 0; i-- {
		ci.InheritedFlags = append(ci.InheritedFlags, anc[i].flagInfos()...)
	}
	for _, a := range c.args {
		ci.Args = append(ci.Args, ArgInfo{
			Name:        a.name,
			Description: a.desc,
			Default:     a.defText,
			HasDefault:  a.hasDef,
			Required:    a.required(),
			Variadic:    a.variadic,
		})
	}
	for _, ch := range c.children {
		ci.Commands = append(ci.Commands, ch.Info())
	}
	return ci
}

func (c *Command) flagInfos() []FlagInfo {
	defs := c.flags
	if c.parent == nil {
		defs = append(slices.Clip(defs), c.app.help)
		if c.app.version != "" {
			defs = append(defs, c.app.versionFlag)
		}
	}
	var fi []FlagInfo
	for _, d := range defs {
		f := FlagInfo{
			Name:        d.name,
			Short:       d.short,
			Env:         d.env,
			Description: d.desc,
			Repeatable:  d.repeatable(),
			Negatable:   d.negatable,
			Default:     d.defText,
			HasDefault:  d.hasDef,
			Choices:     slices.Clone(d.choices),
			Required:    d.required,
			Hidden:      d.hidden,
			Deprecated:  d.deprec,
		}
		if d.takesValue() {
			f.ValueName = d.valueName
		}
		fi = append(fi, f)
	}
	return fi
}

// defaultHelp renders help in this layout:
//
//	<description>
//
//	Usage:
//	  git remote add [flags] <name> [<dir>]
//
//	Aliases / Commands / Arguments / Flags / Inherited flags sections
//
// Hidden and deprecated commands and flags are not listed.
func defaultHelp(w io.Writer, cmd CommandInfo) error {
	var sb strings.Builder
	if d := strings.TrimSpace(cmd.Description); d != "" {
		sb.WriteString(d + "\n\n")
	}
	if cmd.Deprecated != "" {
		sb.WriteString("Deprecated: " + cmd.Deprecated + "\n\n")
	}
	sb.WriteString("Usage:\n  " + usage(cmd) + "\n")
	if len(cmd.Aliases) > 0 {
		sb.WriteString("\nAliases:\n  " + strings.Join(cmd.Aliases, ", ") + "\n")
	}

	var cmds [][2]string
	for _, ch := range cmd.Commands {
		if !ch.Hidden && ch.Deprecated == "" {
			cmds = append(cmds, [2]string{ch.Name, firstLine(ch.Description)})
		}
	}
	section(&sb, "Commands", cmds)

	var args [][2]string
	for _, a := range cmd.Args {
		desc := a.Description
		if a.HasDefault && a.Default != "" {
			desc = annotate(desc, []string{"default: " + a.Default})
		}
		args = append(args, [2]string{a.Name, desc})
	}
	section(&sb, "Arguments", args)
	section(&sb, "Flags", flagRows(cmd.Flags))
	section(&sb, "Inherited flags", flagRows(cmd.InheritedFlags))

	if len(cmds) > 0 {
		fmt.Fprintf(&sb, "\nRun '%s <command> --help' for more information on a command.\n", strings.Join(cmd.Path, " "))
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

func usage(cmd CommandInfo) string {
	parts := slices.Clone(cmd.Path)
	if len(cmd.Commands) > 0 {
		if cmd.Runnable {
			parts = append(parts, "[command]")
		} else {
			parts = append(parts, "<command>")
		}
	}
	parts = append(parts, "[flags]")
	for _, a := range cmd.Args {
		s := "<" + a.Name + ">"
		if a.Variadic {
			s += "..."
		}
		if !a.Required {
			s = "[" + s + "]"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func flagRows(flags []FlagInfo) [][2]string {
	var rows [][2]string
	for _, f := range flags {
		if f.Hidden || f.Deprecated != "" {
			continue
		}
		left := "    "
		if f.Short != 0 {
			left = "-" + string(f.Short) + ", "
		}
		if f.Negatable {
			left += "--[no-]" + f.Name
		} else {
			left += "--" + f.Name
		}
		if f.ValueName != "" {
			left += " " + f.ValueName
		}
		var notes []string
		if len(f.Choices) > 0 {
			notes = append(notes, "one of: "+strings.Join(f.Choices, ", "))
		}
		if f.HasDefault && f.Default != "" {
			notes = append(notes, "default: "+f.Default)
		}
		if f.Env != "" {
			notes = append(notes, "env: "+f.Env)
		}
		if f.Required {
			notes = append(notes, "required")
		}
		if f.Repeatable {
			notes = append(notes, "repeatable")
		}
		rows = append(rows, [2]string{left, annotate(f.Description, notes)})
	}
	return rows
}

func annotate(desc string, notes []string) string {
	desc = firstLine(desc)
	if len(notes) == 0 {
		return desc
	}
	n := "(" + strings.Join(notes, "; ") + ")"
	if desc == "" {
		return n
	}
	return desc + " " + n
}

func section(sb *strings.Builder, title string, rows [][2]string) {
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		width = max(width, len([]rune(r[0])))
	}
	sb.WriteString("\n" + title + ":\n")
	for _, r := range rows {
		if r[1] == "" {
			sb.WriteString("  " + r[0] + "\n")
			continue
		}
		fmt.Fprintf(sb, "  %-*s  %s\n", width, r[0], r[1])
	}
}
