package cxx

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const maxTemplateArguments = 16

// Keep C++ syntax out of the manifest. Aliases and constexpr identifiers can
// name complex types/values without interpreting their grammar or ABI in Go.
func templateArguments(args []string) (string, error) {
	if args == nil {
		return "", nil
	}
	if len(args) > maxTemplateArguments {
		return "", fmt.Errorf("at most %d explicit template arguments are supported", maxTemplateArguments)
	}
	values := make([]string, len(args))
	for i, value := range args {
		if len(value) == 0 || len(value) > maxDiscoveryName {
			return "", fmt.Errorf("invalid or excessive template argument %d", i)
		}
		if qualifiedName(value) || templateBuiltin(value) {
			values[i] = value
			continue
		}
		literal, err := templateInteger(value)
		if err != nil {
			return "", fmt.Errorf("template argument %d: %w", i, err)
		}
		values[i] = literal
	}
	return "<" + strings.Join(values, ", ") + ">", nil
}

func templateBuiltin(value string) bool {
	return nativeBuiltinType(value) || value == "true" || value == "false" || value == "nullptr"
}

func nativeBuiltinType(value string) bool {
	switch value {
	case "bool", "char", "signed", "unsigned", "short", "int", "long", "float", "double", "void", "wchar_t", "char16_t", "char32_t":
		return true
	}
	return false
}

func templateInteger(value string) (string, error) {
	text := value
	negative := strings.HasPrefix(text, "-")
	if negative {
		text = text[1:]
	}
	i := 0
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i == 0 || i > 1 && text[0] == '0' {
		return "", fmt.Errorf("expected a qualified identifier or decimal integer literal")
	}
	suffix := strings.ToUpper(text[i:])
	switch suffix {
	case "", "U", "L", "LL", "UL", "ULL":
	default:
		return "", fmt.Errorf("unsupported integer literal suffix")
	}
	unsigned := strings.HasPrefix(suffix, "U")
	n, err := strconv.ParseUint(text[:i], 10, 64)
	if err != nil || negative && unsigned || negative && n > uint64(1)<<63 || !negative && !unsigned && n > math.MaxInt64 {
		return "", fmt.Errorf("template integer is outside its signed/unsigned 64-bit range")
	}
	if negative && n == uint64(1)<<63 {
		// Its positive magnitude has no standard signed integer literal type.
		return "(-9223372036854775807LL-1)", nil
	}
	if negative {
		return "-" + text[:i] + suffix, nil
	}
	return text[:i] + suffix, nil
}
