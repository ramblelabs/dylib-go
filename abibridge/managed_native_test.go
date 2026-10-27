//go:build cgo && darwin && (amd64 || arm64)

package abibridge

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func testManagedCalls(t *testing.T, facade, native string) {
	t.Run("empty", func(t *testing.T) {
		s := load(t, native)
		r := open(t, facade)
		kind, err := r.PrepareValueType(resolve(t, s, "fixture_empty_metadata"))
		if err != nil {
			t.Fatal(err)
		}
		shape := Type{Kind: Indirect, Alignment: 1, ValueType: kind}
		makeCall := prepare(t, r, Signature{Result: shape})
		echoCall := prepare(t, r, Signature{Result: shape, Args: []Type{shape}})
		readCall := prepare(t, r, Signature{Result: Type{Kind: Int32}, Args: []Type{shape}})
		value, err := makeCall.InvokeManaged(resolve(t, s, "fixture_owned_empty_make"))
		if err != nil {
			t.Fatal(err)
		}
		out, err := echoCall.InvokeManaged(resolve(t, s, "fixture_owned_empty_echo"), Argument{Value: value.Value})
		if err != nil {
			t.Fatal(err)
		}
		value.Value.Close()
		result, err := readCall.InvokeManaged(resolve(t, s, "fixture_owned_empty_read"), Argument{Value: out.Value})
		if err != nil || binary.LittleEndian.Uint32(result.Bytes) != 42 {
			t.Fatal(result, err)
		}
		out.Value.Close()
	})
	for _, mode := range []string{"direct", "indirect", "mixed", "large"} {
		t.Run(mode, func(t *testing.T) {
			for _, name := range []string{"ownership", "alias", "validation", "concurrent", "argument_retirement", "call_retirement", "runtime_retirement", "provider_retirement", "native_validation"} {
				t.Run(name, func(t *testing.T) {
					s := load(t, native)
					r := open(t, facade)
					metadata := "fixture_managed_metadata"
					if mode != "indirect" {
						metadata = "fixture_" + mode + "_metadata"
					}
					kind, err := r.PrepareValueType(resolve(t, s, metadata))
					if err != nil {
						t.Fatal(err)
					}
					layout := kind.Layout()
					shape := Type{Kind: Indirect, Size: uint32(layout.Size), Alignment: uint32(layout.Alignment), ValueType: kind}
					if mode != "indirect" {
						shape.Kind = Storage
						shape.Components = []Component{{Pointer, 0}, {Uint64, 8}, {Uint64, 16}, {Int64, 24}}
						if mode == "mixed" {
							shape.Components[3].Kind = Float64
						}
						if mode == "large" {
							shape.Components = append(shape.Components, Component{Uint64, 32}, Component{Float64, 40})
						}
					}
					makeCall := prepare(t, r, Signature{Result: shape, Args: []Type{{Kind: Int64}}})
					readCall := prepare(t, r, Signature{Result: Type{Kind: Int64}, Args: []Type{shape}})
					echoCall := prepare(t, r, Signature{Result: shape, Args: []Type{shape, {Kind: Int64}}})
					prefix := "fixture_owned_" + mode + "_"
					makeSymbol, readSymbol, echoSymbol := resolve(t, s, prefix+"make"), resolve(t, s, prefix+"read"), resolve(t, s, prefix+"echo")
					count := control(t, s, "fixture_value_count")
					if _, err := count(1, 0); err != nil {
						t.Fatal(err)
					}
					assertCount := func(want int32) {
						t.Helper()
						if got, err := count(0, 0); err != nil || got != want {
							t.Fatal("destroyed", got, "want", want, err)
						}
					}
					makeValue := func() *Value {
						t.Helper()
						out, err := makeCall.InvokeManaged(makeSymbol, Argument{Bytes: words(42)})
						if err != nil || out.Value == nil || len(out.Bytes) != 0 {
							t.Fatal(out, err)
						}
						return out.Value
					}
					readValue := func(value *Value, want uint64) error {
						out, err := readCall.InvokeManaged(readSymbol, Argument{Value: value})
						if err != nil {
							return err
						}
						if out.Value != nil || len(out.Bytes) != 8 || binary.LittleEndian.Uint64(out.Bytes) != want {
							return fmt.Errorf("managed read: %v, want %d", out.Bytes, want)
						}
						return nil
					}
					value := makeValue()
					switch name {
					case "ownership":
						out, err := echoCall.InvokeManaged(echoSymbol, Argument{Value: value}, Argument{Bytes: words(8)})
						if err != nil {
							t.Fatal(err)
						}
						if err := readValue(value, 642); err != nil {
							t.Fatal(err)
						}
						value.Close()
						assertCount(0)
						if err := readValue(out.Value, 654); err != nil {
							t.Fatal(err)
						}
						copy, err := out.Value.Clone()
						if err != nil {
							t.Fatal(err)
						}
						out.Value.Close()
						assertCount(0)
						if err := readValue(copy, 654); err != nil {
							t.Fatal(err)
						}
						copy.Close()
						copy.Close()
						assertCount(1)
					case "alias":
						pair := prepare(t, r, Signature{Result: shape, Args: []Type{shape, shape, {Kind: Int64}}})
						out, err := pair.InvokeManaged(resolve(t, s, prefix+"pair"), Argument{Value: value}, Argument{Value: value}, Argument{Bytes: words(2)})
						if err != nil {
							t.Fatal(err)
						}
						value.Close()
						assertCount(0)
						if err := readValue(out.Value, 1286); err != nil {
							t.Fatal(err)
						}
						out.Value.Close()
						assertCount(1)
					case "validation":
						other, err := r.PrepareValueType(resolve(t, s, metadata))
						if err != nil {
							t.Fatal(err)
						}
						copy, err := value.Clone()
						if err != nil {
							t.Fatal(err)
						}
						copy.Close()
						for _, args := range [][]Argument{nil, {{Bytes: words(42)}}, {{Value: value}, {Bytes: words(1)}}, {{Value: copy}}, {{Value: value, Bytes: words(1)}}} {
							if _, err := readCall.InvokeManaged(readSymbol, args...); err == nil {
								t.Fatal("invalid managed argument accepted")
							}
						}
						foreign := shape
						foreign.ValueType = other
						mismatch := prepare(t, r, Signature{Result: Type{Kind: Int64}, Args: []Type{foreign}})
						if _, err := mismatch.InvokeManaged(readSymbol, Argument{Value: value}); err == nil {
							t.Fatal("different ValueType accepted")
						}
						if _, err := readCall.Invoke(readSymbol, make([]byte, shape.Size)); err == nil {
							t.Fatal("managed representation bytes accepted")
						}
						if _, err := makeCall.InvokeManaged(nil, Argument{Bytes: words(1)}); err == nil {
							t.Fatal("missing function accepted")
						}
						if _, err := echoCall.InvokeManaged(echoSymbol, Argument{Value: value}, Argument{Bytes: []byte{1}}); err == nil {
							t.Fatal("invalid trailing argument accepted after borrowing a value")
						}
						bad := shape
						bad.Size++
						if _, err := r.Prepare(Signature{Result: bad}); err == nil {
							t.Fatal("metadata layout mismatch accepted")
						}
						another := open(t, facade)
						if _, err := another.Prepare(Signature{Result: shape}); err == nil {
							t.Fatal("foreign runtime type accepted")
						}
						if err := readValue(value, 642); err != nil {
							t.Fatal(err)
						}
						value.Close()
						assertCount(1)
					case "concurrent":
						var group sync.WaitGroup
						failures := make(chan error, 8)
						for i := 0; i < 8; i++ {
							group.Add(1)
							go func() {
								defer group.Done()
								for j := 0; j < 8; j++ {
									out, err := echoCall.InvokeManaged(echoSymbol, Argument{Value: value}, Argument{Bytes: words(8)})
									if err == nil {
										err = readValue(out.Value, 654)
										out.Value.Close()
									}
									if err != nil {
										failures <- err
										return
									}
								}
							}()
						}
						group.Wait()
						close(failures)
						for err := range failures {
							t.Error(err)
						}
						assertCount(0)
						value.Close()
						assertCount(1)
					case "native_validation":
						for _, handles := range [][]uint64{nil, {0}, {0, 0}, {0, 0, 0}} {
							err := readSymbol.WithAddress(func(address uintptr) error {
								return nativeInvokeValues(r.invokeValues, readCall.plan, address, nil, make([]byte, 8), handles)
							})
							if err == nil {
								t.Fatal("invalid native buffers accepted")
							}
						}
						// Reject layout mismatch before an uninitialized output can be committed.
						handle, err := nativeValueNew(r.valueOps.newValue, kind.plan, 0)
						if err != nil {
							t.Fatal(err)
						}
						err = readSymbol.WithAddress(func(address uintptr) error {
							return nativeInvokeValues(r.invokeValues, readCall.plan, address, words(1), nil, []uint64{uint64(handle), 0})
						})
						if err == nil {
							t.Fatal("invalid native output accepted")
						}
						err = readSymbol.WithAddress(func(address uintptr) error {
							return nativeInvokeValues(r.invokeValues, readCall.plan, address, nil, make([]byte, 8), []uint64{0, uint64(handle)})
						})
						nativeRelease(r.valueOps.release, handle)
						if err == nil {
							t.Fatal("uninitialized native input accepted")
						}
						assertCount(0)
						value.Close()
						assertCount(1)
					default:
						// An independent observer can release native code after the
						// function provider has rejected new symbol operations.
						gate := control(t, load(t, native), "fixture_gate")
						if _, err := gate(2, 0); err != nil {
							t.Fatal(err)
						}
						defer gate(1, 0)
						wait := prepare(t, r, Signature{Result: shape, Args: []Type{shape}})
						waitSymbol := resolve(t, s, prefix+"wait")
						type returned struct {
							out Result
							err error
						}
						done := make(chan returned, 1)
						go func() { out, err := wait.InvokeManaged(waitSymbol, Argument{Value: value}); done <- returned{out, err} }()
						waitValueState(t, func() bool { state, err := gate(0, 0); return err == nil && state != 0 })
						closed := make(chan error, 1)
						var closeFunc func() error
						switch name {
						case "argument_retirement":
							closeFunc = value.Close
						case "call_retirement":
							closeFunc = wait.Close
						case "runtime_retirement":
							closeFunc = r.Close
						case "provider_retirement":
							closeFunc = s.Close
						}
						go func() { closed <- closeFunc() }()
						switch name {
						case "argument_retirement":
							waitValueState(t, func() bool { value.mu.Lock(); defer value.mu.Unlock(); return value.closing })
						case "runtime_retirement":
							waitValueState(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.closed })
						}
						select {
						case err := <-closed:
							t.Fatal("closed during active call", err)
						case <-time.After(20 * time.Millisecond):
						}
						if _, err := gate(1, 0); err != nil {
							t.Fatal(err)
						}
						var result returned
						select {
						case result = <-done:
						case <-time.After(10 * time.Second):
							t.Fatal("managed call did not complete")
						}
						if name == "runtime_retirement" {
							if !errors.Is(result.err, ErrClosed) || result.out.Value != nil {
								t.Fatal("result published during retirement", result.err)
							}
							waitValueClose(t, closed)
							assertCount(1)
						} else {
							if result.err != nil || result.out.Value == nil {
								t.Fatal(result.out, result.err)
							}
							if name != "provider_retirement" {
								waitValueClose(t, closed)
								if err := readValue(result.out.Value, 642); err != nil {
									t.Fatal(err)
								}
							}
							result.out.Value.Close()
							value.Close()
							if name == "provider_retirement" {
								// Metadata still holds the image until runtime teardown.
								select {
								case err := <-closed:
									t.Fatal("metadata provider released early", err)
								default:
								}
								r.Close()
								waitValueClose(t, closed)
							} else {
								assertCount(1)
							}
							if name == "call_retirement" {
								if _, err := wait.InvokeManaged(waitSymbol, Argument{Value: value}); !errors.Is(err, ErrClosed) {
									t.Fatal(err)
								}
							}
						}
					}
				})
			}
		})
	}
}
