package module

import (
	"errors"
	"fmt"
	"sync"

	dylib "github.com/ramblelabs/dylib-go"
)

// ErrInitializationAborted is returned to later callers when a lazy builder
// panicked or exited without returning. The initializing caller's panic still
// propagates. Initialization is not retried implicitly.
var ErrInitializationAborted = errors.New("module: lazy graph initialization did not complete")

// Lazy owns one graph initialized on first use. Concurrent first users share
// one builder invocation; successful scopes can nest without holding an
// admission lock across callbacks or native calls. Do not copy a Lazy, close
// its graph independently, or retain borrowed graph/session owners beyond a
// scope. Acquired native symbol/function leases may outlive a scope.
type Lazy struct {
	mu      sync.Mutex
	changed *sync.Cond
	once    sync.Once
	build   func() (*Graph, error)
	graph   *Graph // Also retains a failed initialization's cleanup owner.
	err     error
	uses    int
	retired bool
	attempt *closeAttempt
}

// NewLazy defers build until the first WithGraph. It performs no native loading
// itself. The builder must return a fresh, unowned graph; every non-nil returned
// graph transfers to this owner, including on error. Failed candidate cleanup
// is attempted and retained for Close retry when necessary. Initialization errors
// are cached, so later uses cannot replay native side effects or try a fallback.
//
// Builders may explicitly discover, compile and open providers. Their files,
// manifests, options and other captured inputs must stay valid until first use;
// this constructor does not snapshot them. The builder remains responsible for
// resources it never returns, including if it panics before returning.
// Neither build nor its initialization/failure-cleanup callbacks may call
// this same owner's WithGraph/Close: they would wait for their own initialization.
// Close and uses
// may otherwise be concurrent; native effects cannot be undone automatically.
func NewLazy(build func() (*Graph, error)) (*Lazy, error) {
	if build == nil {
		return nil, fmt.Errorf("module: nil lazy graph builder")
	}
	l := &Lazy{build: build}
	l.changed = sync.NewCond(&l.mu)
	return l, nil
}

func (l *Lazy) initialize() {
	completed := false
	defer func() {
		l.mu.Lock()
		l.build = nil
		if !completed {
			l.err = ErrInitializationAborted
		}
		l.mu.Unlock()
	}()
	graph, err := l.build()
	// Own a returned graph before attempting validation or native cleanup.
	l.mu.Lock()
	l.graph = graph
	l.mu.Unlock()
	if err == nil && !graph.isOpen() {
		err = fmt.Errorf("module: lazy builder must return a fresh open graph")
	}
	if err != nil && graph != nil {
		cleanup := graph.Close()
		err = errors.Join(err, cleanup)
		if cleanup == nil {
			graph = nil
		}
	}
	l.mu.Lock()
	l.graph, l.err = graph, err
	completed = true
	l.mu.Unlock()
}

// WithGraph initializes once, then borrows the graph until use returns or panics.
// Nil callbacks are rejected without invoking the builder. Initial callers wait
// for that shared builder, including its failure cleanup; there is no implicit
// initialization retry. Waiting for a builder/OS loader is not cancellable here;
// a builder may capture an explicit context for operations that support it.
//
// Close retires admission immediately. If it races with initialization, pending
// uses cannot borrow the returned graph. In-flight callers receive ErrClosed,
// joined with any initialization error. Call Close outside this owner's scopes,
// builder, and native initialization/finalization callbacks.
func (l *Lazy) WithGraph(use func(*Graph) error) error {
	if l == nil || l.changed == nil {
		return dylib.ErrClosed
	}
	if use == nil {
		return fmt.Errorf("module: nil lazy graph user")
	}
	l.mu.Lock()
	if l.retired {
		l.mu.Unlock()
		return dylib.ErrClosed
	}
	l.mu.Unlock()
	l.once.Do(l.initialize)
	l.mu.Lock()
	if l.retired {
		err := errors.Join(dylib.ErrClosed, l.err)
		l.mu.Unlock()
		return err
	}
	if l.err != nil {
		err := l.err
		l.mu.Unlock()
		return err
	}
	graph := l.graph
	l.uses++
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.uses--
		l.changed.Broadcast()
		l.mu.Unlock()
	}()
	return use(graph)
}

// Close prevents new uses, waits for initialization and scopes, then closes the
// owned graph, respecting external native leases. Before first use it discards
// the builder without invoking it. Concurrent callers share one cleanup attempt.
// Failed cleanup stays owned for a later Close retry; admission stays retired.
// Call outside this owner's own builder/scopes/native callbacks.
func (l *Lazy) Close() error {
	if l == nil || l.changed == nil {
		return nil
	}
	l.mu.Lock()
	if attempt := l.attempt; attempt != nil {
		l.mu.Unlock()
		<-attempt.done
		return attempt.err
	}
	attempt := &closeAttempt{done: make(chan struct{})}
	l.attempt, l.retired = attempt, true
	l.mu.Unlock()
	// Either wait for an existing builder or prevent an unused one from starting.
	l.once.Do(func() {})
	l.mu.Lock()
	for l.uses != 0 {
		l.changed.Wait()
	}
	graph := l.graph
	l.mu.Unlock()
	err := graph.Close()
	l.mu.Lock()
	if err == nil {
		l.graph = nil
	}
	l.build = nil
	attempt.err, l.attempt = err, nil
	close(attempt.done)
	l.mu.Unlock()
	return err
}
