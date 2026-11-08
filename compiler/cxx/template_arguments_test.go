package cxx

import (
	"fmt"
	"strings"
	"testing"
)

func TestTemplateArgumentRendering(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"ordinary", nil, ""}, {"empty", []string{}, "<>"},
		{"type", []string{"int32_t"}, "<int32_t>"},
		{"qualified", []string{"api::Alias", "policy::value"}, "<api::Alias, policy::value>"},
		{"keywords", []string{"true", "nullptr"}, "<true, nullptr>"},
		{"decimal", []string{"0", "-20", "42"}, "<0, -20, 42>"},
		{"unsigned", []string{"18446744073709551615ull"}, "<18446744073709551615ULL>"},
		{"signed_minimum", []string{"-9223372036854775808LL"}, "<(-9223372036854775807LL-1)>"},
		{"signed_maximum", []string{"9223372036854775807LL"}, "<9223372036854775807LL>"},
		{"suffixes", []string{"1u", "2l", "3ll", "4ul", "5ull"}, "<1U, 2L, 3LL, 4UL, 5ULL>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := templateArguments(tc.args)
			if err != nil || got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}

func TestTemplateArgumentValidation(t *testing.T) {
	for i, value := range []string{"", "::api::Type", "Type*", "const Type", "Type<int>", "1,2", "1>", "1;", "1/*x*/", "sizeof(int)", "&api::function", "1+2", " 1", "1 ", "+1", "01", "-01", "0x10", "0b10", "1.5", "1'000", "1lu", "-1U", "18446744073709551616ULL", "9223372036854775808", "-9223372036854775809LL", strings.Repeat("a", 257)} {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			if got, err := templateArguments([]string{value}); err == nil || got != "" {
				t.Fatal("invalid argument accepted", value, got, err)
			}
		})
	}
	t.Run("argument_limit", func(t *testing.T) {
		v := make([]string, 17)
		for i := range v {
			v[i] = "int"
		}
		if got, err := templateArguments(v); err == nil || got != "" {
			t.Fatal(got, err)
		}
	})
	t.Run("input_unchanged", func(t *testing.T) {
		v := []string{"1ull"}
		if _, err := templateArguments(v); err != nil || v[0] != "1ull" {
			t.Fatal(v, err)
		}
	})
}
