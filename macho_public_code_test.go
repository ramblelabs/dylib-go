package dylib

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func machoPublicCodeProviders(t *testing.T, target string) []string {
	t.Helper()
	var paths []string
	for i, name := range []string{"a", "b"} {
		body := fmt.Sprintf(".byte 0x0f,0x1f,0x40,0x00\nleal %d(%%rdi,%%rsi), %%eax\nretq\n", 42*(i+1))
		if strings.HasPrefix(target, "arm64") {
			body = fmt.Sprintf("nop\nadd w0,w0,w1\nadd w0,w0,#%d\nret\n", 42*(i+1))
		}
		paths = append(paths, machoCoalescedAssembly(t, target, ".text\n.p2align 2\n.globl _public_fn_"+name+"\n_public_fn_"+name+":\n"+body))
	}
	return paths
}

func TestNativeMachOPublicAliasCode(t *testing.T) {
	target := needMachOCoalescedNative(t)
	sdk := machoCoalescedSDK(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			providers := machoPublicCodeProviders(t, target)
			for _, kind := range []string{"regular", "coalesced"} {
				t.Run(kind, func(t *testing.T) {
					for form := 0; form < 2; form++ {
						t.Run(fmt.Sprintf("form%d", form), func(t *testing.T) {
							flags := machoPublicFlags(kind, form, opt)
							weak, _ := machoCoalescedFixture(t, target, "macho_public_code.S", flags...)
							probe, _ := machoCoalescedFixture(t, target, "macho_public_code.c", flags...)
							archive := machoCoalescedArchive(t, weak)
							for mode := 0; mode < 4; mode++ {
								t.Run(fmt.Sprintf("override%d", mode), func(t *testing.T) {
									var chosen []string
									for i, p := range providers {
										if mode&(1<<i) != 0 {
											chosen = append(chosen, p)
										}
									}
									for _, order := range []string{"weak_first", "strong_first"} {
										for _, input := range []string{"objects", "archive"} {
											t.Run(order+"/"+input, func(t *testing.T) {
												path := weak
												if input == "archive" {
													path = archive
												}
												inputs := []string{probe, path}
												if order == "strong_first" {
													inputs = append(chosen, inputs...)
												} else {
													inputs = append(inputs, chosen...)
												}
												s := New(Options{})
												defer s.Close()
												load(t, s, inputs...)
												for n := int32(0); n < 5; n++ {
													call(t, s, "public_fn_check", int32(mode), 42+n, 42+n)
												}
											})
										}
									}
									t.Run("system_control", func(t *testing.T) {
										inputs := append([]string{weak}, chosen...)
										if opt == "-O2" {
											inputs = append(chosen, weak)
										}
										exe := filepath.Join(t.TempDir(), "control")
										args := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, "-Wl,-dead_strip", "testdata/macho_public_code.c", "-DDYLIB_PUBLIC_STANDALONE", fmt.Sprintf("-DDYLIB_PUBLIC_OVERRIDE=%d", mode)}
										args = append(args, flags...)
										args = append(args, inputs...)
										command(t, compiler(), append(args, "-o", exe)...)
										command(t, exe)
									})
								})
							}
						})
					}
				})
			}
		})
	}
}
