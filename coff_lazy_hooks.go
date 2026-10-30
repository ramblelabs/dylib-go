package dylib

import (
	"errors"
	"syscall"
)

// DelayImportEvent identifies a step in raw COFF lazy import resolution.
// Complete PE DLLs continue to use the operating system and their own helpers.
type DelayImportEvent uint8

const (
	DelayImportStartProcessing DelayImportEvent = iota
	DelayImportBeforeLoadLibrary
	DelayImportBeforeGetProcAddress
	DelayImportLoadLibraryFailure
	DelayImportGetProcAddressFailure
	DelayImportEndProcessing
)

// DelayImportInfo is a value snapshot. NativeError preserves a native loader
// error when available; Err describes path, loader or lookup failures.
type DelayImportInfo struct {
	DLL, Symbol, Name string
	Ordinal           uint16
	Module, Address   uintptr
	NativeError       uint32
	Err               error
}

// DelayImportResult supplies a native module or callable address. Start can
// return Address to bypass lookup, optionally with Module. BeforeLoadLibrary
// and LoadLibraryFailure accept Module; the two GetProcAddress events accept
// Address. EndProcessing ignores the result and transfers no ownership.
// Module participates in the per-DLL canonical cache; a direct-target result
// must agree with an already selected module for that DLL.
//
// OwnModule transfers exactly one acquired library reference to the image.
// Otherwise the caller retains Module and code used by Address until session
// cleanup or an explicit unload resets that entry. Addresses must use the actual
// native calling convention; an ordinary Go function address is not sufficient.
type DelayImportResult struct {
	Module, Address uintptr
	OwnModule       bool
}

// DelayImportHooks supplies Go hooks for raw lazy imports. Hooks can run on
// foreign native threads and concurrently on first calls. They run without
// session/provider locks and can reenter other imports. Recursion into the
// same unresolved import on one native thread fails with native error 87.
// A hook panic becomes a native invalid-parameter failure after Go returns.
// This API does not implement the binary __pfnDli*Hook2 variable convention.
type DelayImportHooks struct {
	Notify  func(DelayImportEvent, DelayImportInfo) DelayImportResult
	Failure func(DelayImportEvent, DelayImportInfo) DelayImportResult
}

func nativeDelayError(err error, fallback uint32) uint32 {
	var number syscall.Errno
	if errors.As(err, &number) && number != 0 {
		return uint32(number)
	}
	return fallback
}

func delayProviderHooks(hooks DelayImportHooks) coffLazyHooks {
	adapt := func(hook func(DelayImportEvent, DelayImportInfo) DelayImportResult) func(coffLazyEvent, coffLazyInfo) coffLazyResult {
		if hook == nil {
			return nil
		}
		return func(event coffLazyEvent, info coffLazyInfo) coffLazyResult {
			var fallback uint32
			if event == coffLazyLoadFailure {
				fallback = coffDelayMissingModule
			} else if event == coffLazyLookupFailure {
				fallback = coffDelayMissingProcedure
			}
			result := hook(DelayImportEvent(event), DelayImportInfo{
				DLL: info.dll, Symbol: info.symbol, Name: info.name, Ordinal: info.ordinal,
				Module: info.module, Address: info.address, NativeError: nativeDelayError(info.err, fallback), Err: info.err,
			})
			return coffLazyResult{module: result.Module, address: result.Address, owned: result.OwnModule}
		}
	}
	return coffLazyHooks{notify: adapt(hooks.Notify), failure: adapt(hooks.Failure)}
}

// ErrDelayImportsBusy means explicit unload cannot start while a managed native
// call, symbol lease, initialization or another unload is active.
var ErrDelayImportsBusy = errors.New("dylib: delayed imports are in use")

// UnloadDelayImports resets raw lazy import caches and releases their owned
// library references, even with KeepLibraries. Explicitly loaded and borrowed
// modules stay owned by their original owners. Later calls resolve again.
// Hook-borrowed handles are forgotten; explicitly loaded matching DLLs remain
// borrowed from the session and are available for subsequent resolutions.
//
// Stop unmanaged native callers and callbacks before invoking this method.
// Managed calls and leases make it return ErrDelayImportsBusy instead of
// waiting. Call outside this session's adapters, hooks and native finalizers.
// A release error retains the failed reference and restores its cached targets;
// retry after resolving the native cleanup failure.
func (s *Session) UnloadDelayImports() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if s.initErr != nil {
		s.mu.Unlock()
		return s.initErr
	}
	if s.initializing || s.unloading || s.calls != 0 {
		s.mu.Unlock()
		return ErrDelayImportsBusy
	}
	if s.image == nil || s.image.lazy == nil {
		s.mu.Unlock()
		return nil
	}
	lazy := s.image.lazy
	s.unloading = true
	s.calls++ // A concurrent Close must retain the image during FreeLibrary.
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.unloading = false
		s.calls--
		if s.calls == 0 && s.cond != nil {
			s.cond.Broadcast()
		}
		s.mu.Unlock()
	}()
	return lazy.providers.unload()
}
