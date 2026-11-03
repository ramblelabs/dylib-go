package module

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dylib "github.com/ramblelabs/dylib-go"
)

func waitLazyRetired(t *testing.T, l *Lazy) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		l.mu.Lock()
		retired := l.retired
		l.mu.Unlock()
		if retired {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("lazy admission did not retire")
		}
		runtime.Gosched()
	}
}
func receiveLazy(t *testing.T, c <-chan error) error {
	t.Helper()
	select {
	case err := <-c:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("lazy operation did not complete")
		return nil
	}
}

func TestLazyUnusedClose(t *testing.T) {
	var calls atomic.Int32
	l, err := NewLazy(func() (*Graph, error) { calls.Add(1); return retryGraph(), nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("constructor initialized graph")
	}
	if err := l.WithGraph(nil); err == nil {
		t.Fatal("nil scope accepted")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.WithGraph(func(*Graph) error { t.Fatal("closed owner admitted use"); return nil }); !errors.Is(err, dylib.ErrClosed) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("closing an unused owner invoked builder")
	}
}

func TestLazyConcurrentInitialization(t *testing.T) {
	var builds, closes atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	graph := retryGraph(closeFunc(func() error { closes.Add(1); return nil }))
	l, _ := NewLazy(func() (*Graph, error) { builds.Add(1); close(started); <-release; return graph, nil })
	t.Cleanup(func() { l.Close() })
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	const n = 16
	results := make(chan error, n)
	var ready sync.WaitGroup
	ready.Add(n)
	for range n {
		go func() {
			ready.Done()
			results <- l.WithGraph(func(g *Graph) error {
				if g != graph {
					return fmt.Errorf("different graph published")
				}
				return nil
			})
		}()
	}
	ready.Wait()
	<-started
	if builds.Load() != 1 {
		t.Fatal("concurrent builders", builds.Load())
	}
	close(release)
	for range n {
		if err := receiveLazy(t, results); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 1 || closes.Load() != 1 {
		t.Fatal("initialization/close count", builds.Load(), closes.Load())
	}
}

func TestLazyScopesReentryAndPanic(t *testing.T) {
	var closed atomic.Int32
	graph := retryGraph(closeFunc(func() error { closed.Add(1); return nil }))
	l, _ := NewLazy(func() (*Graph, error) { return graph, nil })
	failure := errors.New("scope error")
	if err := l.WithGraph(func(g *Graph) error {
		return l.WithGraph(func(nested *Graph) error {
			if nested != g {
				t.Fatal("reentry changed graph")
			}
			return failure
		})
	}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if got := recover(); got != "scope panic" {
				t.Fatal("scope panic lost", got)
			}
		}()
		l.WithGraph(func(*Graph) error { panic("scope panic") })
	}()
	entered, release := make(chan struct{}), make(chan struct{})
	used := make(chan error, 1)
	go func() { used <- l.WithGraph(func(*Graph) error { close(entered); <-release; return nil }) }()
	<-entered
	finished := make(chan error, 2)
	go func() { finished <- l.Close() }()
	waitLazyRetired(t, l)
	go func() { finished <- l.Close() }()
	if closed.Load() != 0 {
		t.Fatal("cleanup crossed active scope")
	}
	if err := l.WithGraph(func(*Graph) error { t.Fatal("retired owner admitted use"); return nil }); !errors.Is(err, dylib.ErrClosed) {
		t.Fatal(err)
	}
	close(release)
	if err := receiveLazy(t, used); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := receiveLazy(t, finished); err != nil {
			t.Fatal(err)
		}
	}
	if closed.Load() != 1 {
		t.Fatal("scope release/cleanup count", closed.Load())
	}
}

func TestLazyCloseDuringBuild(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var closed atomic.Int32
			graph := retryGraph(closeFunc(func() error { closed.Add(1); return nil }))
			failure := errors.New("builder failed")
			l, _ := NewLazy(func() (*Graph, error) {
				close(started)
				<-release
				if failed {
					return graph, failure
				}
				return graph, nil
			})
			used := make(chan error, 1)
			go func() {
				used <- l.WithGraph(func(*Graph) error { t.Error("close allowed candidate publication"); return nil })
			}()
			<-started
			finished := make(chan error, 1)
			go func() { finished <- l.Close() }()
			waitLazyRetired(t, l)
			if err := l.WithGraph(func(*Graph) error { t.Error("retired owner admitted use"); return nil }); !errors.Is(err, dylib.ErrClosed) {
				t.Fatal(err)
			}
			close(release)
			err := receiveLazy(t, used)
			if !errors.Is(err, dylib.ErrClosed) || failed && !errors.Is(err, failure) {
				t.Fatal("lost close/build error", err)
			}
			if err := receiveLazy(t, finished); err != nil {
				t.Fatal(err)
			}
			if closed.Load() != 1 {
				t.Fatal("unpublished graph cleanup count", closed.Load())
			}
		})
	}
}

func TestLazyInitializationErrorsAreSticky(t *testing.T) {
	for _, mode := range []string{"nil_graph", "closed_graph", "build_error", "error_with_graph"} {
		t.Run(mode, func(t *testing.T) {
			var builds, closes atomic.Int32
			failure := errors.New("load failed")
			graph := retryGraph(closeFunc(func() error { closes.Add(1); return nil }))
			if mode == "closed_graph" {
				if err := graph.Close(); err != nil {
					t.Fatal(err)
				}
			}
			l, _ := NewLazy(func() (*Graph, error) {
				builds.Add(1)
				switch mode {
				case "nil_graph":
					return nil, nil
				case "closed_graph":
					return graph, nil
				case "build_error":
					return nil, failure
				default:
					return graph, failure
				}
			})
			for range 3 {
				err := l.WithGraph(func(*Graph) error { t.Error("failed initialization published graph"); return nil })
				if err == nil || (mode == "build_error" || mode == "error_with_graph") && !errors.Is(err, failure) {
					t.Fatal(err)
				}
			}
			if builds.Load() != 1 {
				t.Fatal("initialization implicitly retried", builds.Load())
			}
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			want := int32(0)
			if mode == "closed_graph" || mode == "error_with_graph" {
				want = 1
			}
			if closes.Load() != want {
				t.Fatal("failed candidate cleanup", closes.Load(), want)
			}
		})
	}
}

func TestLazyBuilderAbortUnblocksWaiters(t *testing.T) {
	for _, mode := range []string{"panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			started, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
			panics := make(chan any, 1)
			var builds atomic.Int32
			l, _ := NewLazy(func() (*Graph, error) {
				builds.Add(1)
				close(started)
				<-release
				if mode == "goexit" {
					runtime.Goexit()
				}
				panic("builder panic")
			})
			go func() {
				defer close(exited)
				defer func() { panics <- recover() }()
				l.WithGraph(func(*Graph) error { return nil })
			}()
			<-started
			waiting := make(chan error, 1)
			go func() {
				waiting <- l.WithGraph(func(*Graph) error { t.Error("aborted builder published graph"); return nil })
			}()
			close(release)
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				t.Fatal("initializer did not exit")
			}
			caught := <-panics
			if mode == "panic" && caught != "builder panic" || mode == "goexit" && caught != nil {
				t.Fatal("builder panic/exit changed", caught)
			}
			if err := receiveLazy(t, waiting); !errors.Is(err, ErrInitializationAborted) {
				t.Fatal("waiter lost aborted result", err)
			}
			if err := l.WithGraph(func(*Graph) error { return nil }); !errors.Is(err, ErrInitializationAborted) {
				t.Fatal(err)
			}
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			if builds.Load() != 1 {
				t.Fatal("aborted builder replayed")
			}
		})
	}
}

func TestLazyCleanupRetry(t *testing.T) {
	for _, failedBuild := range []bool{false, true} {
		t.Run(fmt.Sprint(failedBuild), func(t *testing.T) {
			primary, cleanup := errors.New("load failed"), errors.New("native cleanup failed")
			providerCalls, consumerCalls, builds := 0, 0, 0
			graph := retryGraph(closeFunc(func() error { providerCalls++; return nil }), closeFunc(func() error { consumerCalls++; return cleanup }))
			l, _ := NewLazy(func() (*Graph, error) {
				builds++
				if failedBuild {
					return graph, primary
				}
				return graph, nil
			})
			err := l.WithGraph(func(*Graph) error { return nil })
			if failedBuild && (!errors.Is(err, primary) || !errors.Is(err, cleanup)) || !failedBuild && err != nil {
				t.Fatal(err)
			}
			if err := l.Close(); !errors.Is(err, cleanup) {
				t.Fatal("cleanup error lost", err)
			}
			if providerCalls != 0 {
				t.Fatal("failed consumer released provider")
			}
			if err := l.WithGraph(func(*Graph) error { t.Error("retired lazy admitted scope"); return nil }); !errors.Is(err, dylib.ErrClosed) {
				t.Fatal(err)
			}
			cleanup = nil
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			want := 2
			if failedBuild {
				want = 3
			}
			if providerCalls != 1 || consumerCalls != want || builds != 1 {
				t.Fatal("retry repeated/lost ownership", providerCalls, consumerCalls, builds)
			}
		})
	}
}

func TestLazyNilAndZeroValues(t *testing.T) {
	if got, err := NewLazy(nil); got != nil || err == nil {
		t.Fatal(got, err)
	}
	var absent *Lazy
	var zero Lazy
	for _, l := range []*Lazy{absent, &zero} {
		if err := l.WithGraph(func(*Graph) error { t.Error("nil/zero owner admitted use"); return nil }); !errors.Is(err, dylib.ErrClosed) {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLazyFinalizerReentry(t *testing.T) {
	var owner *Lazy
	graph := retryGraph(closeFunc(func() error {
		if err := owner.WithGraph(func(*Graph) error { return errors.New("finalizer admitted a retired scope") }); !errors.Is(err, dylib.ErrClosed) {
			return fmt.Errorf("finalizer reentry: %w", err)
		}
		return nil
	}))
	owner, _ = NewLazy(func() (*Graph, error) { return graph, nil })
	if err := owner.WithGraph(func(*Graph) error { return nil }); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- owner.Close() }()
	if err := receiveLazy(t, closed); err != nil {
		t.Fatal(err)
	}
}
