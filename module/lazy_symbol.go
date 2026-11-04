package module

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	dylib "github.com/ramblelabs/dylib-go"
)

// SymbolGraphBuilder loads the exact manifest selected for one symbol contract.
// It must return a fresh, unowned graph. Every non-nil graph transfers, including
// on error, under NewLazy's ownership rules. Open and OpenCompiled can provide
// ordinary or compiler-backed loading. Captured inputs must remain valid until
// initialization, and unreturned resources remain the builder's responsibility.
type SymbolGraphBuilder func(context.Context, SymbolSelection) (*Graph, error)

// LazySymbol selects and loads one symbol's provider graph on first use. Each
// owner has an independent selection/graph and cached failure; separate owners
// do not pool provider sessions. Native OS library sharing keeps its usual rules.
// Do not copy a LazySymbol or close its graph/sessions independently.
type LazySymbol struct {
	owner    *Lazy
	symbol   string
	provider string // Written during the owner's shared initialization.
}

// NewLazySymbol defers catalog discovery and build until the first WithSymbol.
// It validates request/arguments and freezes absolute directory paths without
// reading catalogs or native inputs. Each selected root and its dependencies
// load as one graph; unrelated symbols need their own explicit owners.
//
// First use applies DiscoverSymbols' exact version, ambiguity and catalog bounds,
// then calls build and verifies the selected export before publishing a scope.
// Discovery/build/export failures are cached. Context controls initialization,
// is passed to build and is checked before publication; cancellation cannot undo
// native side effects or interrupt an OS loader. After initialization succeeds,
// canceling this context does not retire the owner. Use Close for retirement.
func NewLazySymbol(ctx context.Context, request SymbolRequirement, build SymbolGraphBuilder, directories ...string) (*LazySymbol, error) {
	if build == nil {
		return nil, fmt.Errorf("module: nil symbol graph builder")
	}
	if err := discoveryInputs(ctx, 1, len(directories)); err != nil {
		return nil, err
	}
	if err := request.validate(); err != nil {
		return nil, err
	}
	paths := make([]string, len(directories))
	for i, dir := range directories {
		if !textName(dir) {
			return nil, fmt.Errorf("module: invalid catalog directory")
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		paths[i] = abs
	}
	l := &LazySymbol{symbol: request.Symbol}
	owner, err := NewLazy(func() (*Graph, error) {
		selection, err := DiscoverSymbols(ctx, []SymbolRequirement{request}, paths...)
		if err != nil {
			return nil, err
		}
		// Freeze the binding before handing the selection to application code.
		provider := selection.Symbols[0].Provider
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		graph, err := build(ctx, selection)
		if err = errors.Join(err, ctx.Err()); err != nil {
			return graph, err
		}
		if !graph.isOpen() {
			return graph, fmt.Errorf("module: symbol builder must return a fresh open graph")
		}
		session, err := graph.Session(provider)
		if err != nil {
			return graph, err
		}
		if _, err := session.Resolve(request.Symbol); err != nil {
			return graph, err
		}
		l.provider = provider
		return graph, nil
	})
	if err != nil {
		return nil, err
	}
	l.owner = owner
	return l, nil
}

// WithSymbol initializes once and borrows the selected function/data Symbol
// through use. The callback supplies its native signature/adapter. Acquired
// symbol/function leases may outlive the scope. Loaded scopes may nest. Keep
// the borrowed Symbol within the callback, acquiring a lease for later use.
// Call Close outside this owner's scopes/native callbacks. The builder and its
// initialization/failure-cleanup callbacks must not call this same owner's
// WithSymbol or Close. NewLazy's reentry, panic and cleanup rules apply.
// A nil callback is rejected without discovery or loading.
func (l *LazySymbol) WithSymbol(use func(*dylib.Symbol) error) error {
	if l == nil || l.owner == nil {
		return dylib.ErrClosed
	}
	if use == nil {
		return fmt.Errorf("module: nil lazy symbol user")
	}
	return l.owner.WithGraph(func(graph *Graph) error {
		session, err := graph.Session(l.provider)
		if err != nil {
			return err
		}
		symbol, err := session.Resolve(l.symbol)
		if err != nil {
			return err
		}
		return use(symbol)
	})
}

// Close retires admission and drains initialization, scopes and external leases.
// An unused owner never discovers catalogs or invokes its builder. Failed graph
// cleanup remains owned for retry. Nil/zero owners can be closed harmlessly.
func (l *LazySymbol) Close() error {
	if l == nil {
		return nil
	}
	return l.owner.Close()
}
