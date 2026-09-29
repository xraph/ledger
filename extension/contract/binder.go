package contract

import (
	"context"
	"fmt"

	dash "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"

	ledger "github.com/xraph/ledger"
)

// handlerFn is the body of an intent handler. It receives a running engine and
// a resolved scope, and returns engine errors as they are; run translates them.
type handlerFn[I, O any] func(ctx context.Context, eng *ledger.Ledger, sc scope, in I) (O, error)

// run adapts a handlerFn to the dispatcher's typed signature. It is the only
// way a handler is bound, so every intent resolves its scope and translates its
// errors the same way.
func run[I, O any](deps Deps, fn handlerFn[I, O]) func(context.Context, I, dash.Principal) (O, error) {
	return func(ctx context.Context, in I, p dash.Principal) (O, error) {
		var zero O

		eng := deps.engine()
		if eng == nil {
			return zero, &dash.Error{Code: dash.CodeUnavailable, Message: "the ledger engine is not running"}
		}

		sc, err := resolveScope(p, deps)
		if err != nil {
			return zero, err
		}

		out, err := fn(ctx, eng, sc, in)
		if err != nil {
			return zero, toContractError(err)
		}
		return out, nil
	}
}

// binder registers intents and records the kind of each, so a test can prove
// every intent declared in the manifest has a handler of the declared kind.
type binder struct {
	d     *dispatcher.Dispatcher
	deps  Deps
	kinds map[string]string
	err   error
}

func newBinder(d *dispatcher.Dispatcher, deps Deps) *binder {
	return &binder{d: d, deps: deps, kinds: map[string]string{}}
}

func query[I, O any](b *binder, name string, fn handlerFn[I, O]) {
	if b.err != nil {
		return
	}
	if err := dispatcher.RegisterQuery(b.d, contributorName, name, 1, run(b.deps, fn)); err != nil {
		b.err = fmt.Errorf("ledger/contract: register %s: %w", name, err)
		return
	}
	b.kinds[name] = "query"
}

func command[I, O any](b *binder, name string, fn handlerFn[I, O]) {
	if b.err != nil {
		return
	}
	if err := dispatcher.RegisterCommand(b.d, contributorName, name, 1, run(b.deps, fn)); err != nil {
		b.err = fmt.Errorf("ledger/contract: register %s: %w", name, err)
		return
	}
	b.kinds[name] = "command"
}
