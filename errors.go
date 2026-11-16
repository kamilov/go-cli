package cli

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// InputError reports a problem with the user's input: an unknown command or
// flag, a malformed or out-of-range value, a missing required value or a
// failed check. Its message is a single line.
type InputError struct {
	// Path is the command path from the root, for example
	// ["git", "remote", "add"], so the caller can point at the right help.
	Path []string
	Err  error
}

func (e *InputError) Error() string { return e.Err.Error() }

func (e *InputError) Unwrap() error { return e.Err }

// SchemaError reports a mistake in the declarations of the command tree.
// Run, Complete and Err return all of them joined with errors.Join.
type SchemaError struct {
	// Pos is the declaration site as "file.go:line".
	Pos string
	Msg string
}

func (e *SchemaError) Error() string {
	if e.Pos == "" {
		return e.Msg
	}
	return e.Pos + ": " + e.Msg
}

// callerPos returns "file.go:line" of the function skip frames above the
// caller of callerPos.
func callerPos(skip int) string {
	_, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s:%d", filepath.Base(file), line)
}

func inputErrorf(c *Command, format string, args ...any) error {
	return &InputError{Path: c.path(), Err: fmt.Errorf(format, args...)}
}

// suggest returns the candidate closest to input, or "" if none is close
// enough. Distance counts insertions, deletions, substitutions and swaps of
// adjacent characters; up to one edit is tolerated for inputs of two to four
// characters and up to two for longer ones. Shorter inputs get no suggestion:
// one edit away from them is almost any short name. Ties go to the candidate
// declared first.
func suggest(input string, candidates []string) string {
	n := utf8.RuneCountInString(input)
	if n < 2 {
		return ""
	}
	limit := 2
	if n <= 4 {
		limit = 1
	}
	best, bestDist := "", limit+1
	for _, c := range candidates {
		if d := editDistance(strings.ToLower(input), strings.ToLower(c)); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// editDistance is the optimal string alignment distance between a and b.
func editDistance(a, b string) int {
	s, t := []rune(a), []rune(b)
	prev2 := make([]int, len(t)+1)
	prev := make([]int, len(t)+1)
	cur := make([]int, len(t)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(s); i++ {
		cur[0] = i
		for j := 1; j <= len(t); j++ {
			cost := 1
			if s[i-1] == t[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && s[i-1] == t[j-2] && s[i-2] == t[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(t)]
}
