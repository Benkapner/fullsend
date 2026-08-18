//go:build js

package mintcore

import (
	"fmt"
	"syscall/js"
)

var registeredHTTPDoer HTTPDoer

// RegisterHTTP stores a JS fetch callback as the package-level HTTP
// client. The callback is wrapped in a HostFetchDoer. It must be
// called once from mintcoreInitMint before NewHandler is constructed.
func RegisterHTTP(fetchFn js.Value) error {
	if fetchFn.IsUndefined() || fetchFn.IsNull() {
		return fmt.Errorf("fetch callback must not be null or undefined")
	}
	if fetchFn.Type() != js.TypeFunction {
		return fmt.Errorf("fetch callback must be a function, got %s", fetchFn.Type())
	}
	doer, err := NewHostFetchDoer(fetchFn)
	if err != nil {
		return fmt.Errorf("wrapping fetch callback: %w", err)
	}
	registeredHTTPDoer = doer
	return nil
}

// mintHTTP returns the registered HTTP client. On WASM this is the
// HostFetchDoer created by RegisterHTTP.
func mintHTTP() HTTPDoer {
	return registeredHTTPDoer
}

// MintHTTPForInit returns the registered HTTP client for use by the
// WASM entrypoint when constructing verifiers. Call RegisterHTTP first.
func MintHTTPForInit() HTTPDoer {
	return registeredHTTPDoer
}
