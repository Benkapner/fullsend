//go:build js

package mintcore

import (
	"fmt"
	"syscall/js"
)

// awaitPromise blocks until a JS Promise resolves or rejects.
func awaitPromise(promise js.Value) (js.Value, error) {
	ch := make(chan js.Value, 1)
	errCh := make(chan error, 1)

	thenFn := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		ch <- args[0]
		return nil
	})
	defer thenFn.Release()

	catchFn := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		errCh <- fmt.Errorf("%s", args[0].String())
		return nil
	})
	defer catchFn.Release()

	promise.Call("then", thenFn).Call("catch", catchFn)

	select {
	case v := <-ch:
		return v, nil
	case err := <-errCh:
		return js.Value{}, err
	}
}
