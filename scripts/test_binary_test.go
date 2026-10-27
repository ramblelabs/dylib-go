package scripts

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var runnerNames = []string{"TestAlpha", "TestBeta", "TestGamma", "TestDelta", "TestEpsilon", "TestZeta", "FuzzSeed", "Example", "Example_value"}

type runnerRecord struct {
	Names []string
	Args  []string
}

// Reuse the test executable as a tiny stand-in; the runner tests do not invoke
// either compiler or depend on any native object-loader feature.
func TestMain(m *testing.M) {
	if os.Getenv("DYLIB_BINARY_RUNNER_MOCK") == "1" {
		os.Exit(runMockBinary())
	}
	os.Exit(m.Run())
}

func runMockBinary() int {
	names := runnerNames
	if os.Getenv("DYLIB_BINARY_RUNNER_SINGLE") == "1" {
		names = names[:1]
	}
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-test.list") {
			if os.Getenv("DYLIB_BINARY_RUNNER_EMPTY") == "1" {
				fmt.Println("not a test")
				return 0
			}
			fmt.Print(strings.Join(names, "\r\n") + "\r\n")
			return 0
		}
	}
	pattern := ""
	for _, arg := range os.Args[1:] {
		if value, ok := strings.CutPrefix(arg, "-test.run="); ok {
			pattern = value
		}
	}
	expression, err := regexp.Compile(pattern)
	if err != nil || pattern == "" {
		return 2
	}
	record := runnerRecord{Args: os.Args[1:]}
	status := 0
	for _, name := range names {
		if !expression.MatchString(name) {
			continue
		}
		record.Names = append(record.Names, name)
		if os.Getenv("DYLIB_BINARY_RUNNER_FAIL") == name {
			fmt.Println("--- FAIL:", name)
			status = 1
		} else {
			fmt.Println("--- PASS:", name)
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return 2
	}
	path := filepath.Join(os.Getenv("DYLIB_BINARY_RUNNER_RECORDS"), strconv.Itoa(os.Getpid())+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return 2
	}
	return status
}

func TestBinaryRunner(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("test-binary.sh requires Bash:", err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shells := []string{bash}
	if _, err := os.Stat("/bin/bash"); err == nil && bash != "/bin/bash" {
		shells = append(shells, "/bin/bash")
	}
	for _, shell := range shells {
		t.Run(filepath.Base(filepath.Dir(shell)), func(t *testing.T) {
			for _, mode := range []string{"success", "failure", "empty", "single"} {
				t.Run(mode, func(t *testing.T) {
					directory := t.TempDir()
					command := exec.Command(shell, "test-binary.sh", filepath.ToSlash(binary), "3m")
					command.Env = append(os.Environ(), "DYLIB_BINARY_RUNNER_MOCK=1", "DYLIB_BINARY_RUNNER_RECORDS="+directory, "RUNNER_TEMP="+filepath.ToSlash(directory))
					if mode == "failure" {
						command.Env = append(command.Env, "DYLIB_BINARY_RUNNER_FAIL=TestGamma")
					}
					if mode == "empty" {
						command.Env = append(command.Env, "DYLIB_BINARY_RUNNER_EMPTY=1")
					}
					if mode == "single" {
						command.Env = append(command.Env, "DYLIB_BINARY_RUNNER_SINGLE=1")
					}
					output, err := command.CombinedOutput()
					if (err == nil) != (mode == "success" || mode == "single") {
						t.Fatalf("unexpected runner exit: %v\n%s", err, output)
					}
					if paths, err := filepath.Glob(filepath.Join(directory, "dylib-tests.*")); err != nil || len(paths) != 0 {
						t.Fatal("runner leaked temporary files", paths, err, string(output))
					}
					if mode == "empty" {
						if !strings.Contains(string(output), "No tests discovered") {
							t.Fatalf("missing empty-list diagnostic:\n%s", output)
						}
						return
					}
					files, err := filepath.Glob(filepath.Join(directory, "*.json"))
					count := 4
					if mode == "single" {
						count = 1
					}
					if err != nil || len(files) != count {
						t.Fatal("runner did not join all four batches:", files, err, string(output))
					}
					var names []string
					for _, file := range files {
						data, err := os.ReadFile(file)
						if err != nil {
							t.Fatal(err)
						}
						var record runnerRecord
						if err := json.Unmarshal(data, &record); err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(strings.Join(record.Args, " "), "-test.timeout=3m") {
							t.Fatal("timeout not forwarded:", record.Args)
						}
						names = append(names, record.Names...)
					}
					want := append([]string(nil), runnerNames...)
					if mode == "single" {
						want = want[:1]
					}
					sort.Strings(names)
					sort.Strings(want)
					if !reflect.DeepEqual(names, want) {
						t.Fatalf("tests missing or repeated: %v, want %v", names, want)
					}
				})
			}
		})
	}
}
