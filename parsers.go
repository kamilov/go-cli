package cli

import (
	"encoding"
	"errors"
	"strconv"
	"time"
)

// FromString converts a string to a named string type:
// b.Flag(&a.Format, "format").Parse(cli.FromString).
func FromString[T ~string](s string) (T, error) { return T(s), nil }

type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// FromInt converts a base-10 string to an integer type, named or not, and
// rejects values outside the range of the type.
func FromInt[T integer](s string) (T, error) {
	var zero T
	bits := 0
	for v := ^zero; v != 0; v <<= 1 {
		bits++
	}
	if ^zero < 0 { // all bits set is negative only for signed types
		n, err := strconv.ParseInt(s, 10, bits)
		return T(n), numError(err)
	}
	n, err := strconv.ParseUint(s, 10, bits)
	return T(n), numError(err)
}

func parseBool(s string) (bool, error) {
	v, err := strconv.ParseBool(s)
	return v, numError(err)
}

func parseFloat32(s string) (float32, error) {
	v, err := strconv.ParseFloat(s, 32)
	return float32(v), numError(err)
}

func parseFloat64(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	return v, numError(err)
}

// numError drops the strconv function name and the repeated input from the
// message, leaving "invalid syntax" or "value out of range".
func numError(err error) error {
	if ne, ok := errors.AsType[*strconv.NumError](err); ok {
		return ne.Err
	}
	return err
}

// builtinParser picks the parser for the exact type of the field, then falls
// back to encoding.TextUnmarshaler. It returns nil if neither applies. The
// second result is the value placeholder shown in help.
func builtinParser[T any](p *T) (func(string) (T, error), string) {
	var f any
	name := ""
	switch any(p).(type) {
	case *string:
		f, name = FromString[string], "string"
	case *bool:
		f, name = parseBool, "bool"
	case *int:
		f, name = FromInt[int], "int"
	case *int8:
		f, name = FromInt[int8], "int"
	case *int16:
		f, name = FromInt[int16], "int"
	case *int32:
		f, name = FromInt[int32], "int"
	case *int64:
		f, name = FromInt[int64], "int"
	case *uint:
		f, name = FromInt[uint], "uint"
	case *uint8:
		f, name = FromInt[uint8], "uint"
	case *uint16:
		f, name = FromInt[uint16], "uint"
	case *uint32:
		f, name = FromInt[uint32], "uint"
	case *uint64:
		f, name = FromInt[uint64], "uint"
	case *float32:
		f, name = parseFloat32, "float"
	case *float64:
		f, name = parseFloat64, "float"
	case *time.Duration:
		f, name = time.ParseDuration, "duration"
	}
	if f != nil {
		return f.(func(string) (T, error)), name
	}
	if _, ok := any(p).(encoding.TextUnmarshaler); ok {
		return func(s string) (T, error) {
			var v T
			err := any(&v).(encoding.TextUnmarshaler).UnmarshalText([]byte(s))
			return v, err
		}, "value"
	}
	return nil, "value"
}

func valueName[T any](p *T) string {
	_, name := builtinParser(p)
	return name
}
