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

// scopePolicy says whether an intent may run from the empty scope.
type scopePolicy int

const (
	// appRequired is the default: the request must resolve to a real app.
	appRequired scopePolicy = iota
	// platformScope also lets the empty scope through. The empty scope is the
	// platform, not an app, so only an intent written to handle it (the
	// feature catalog, whose global rows belong to no app) opts in.
	platformScope
)

// run adapts a handlerFn to the dispatcher's typed signature. It is the only
// way a handler is bound, so every intent resolves its scope and translates its
// errors the same way. The handler runs for a real app only.
func run[I, O any](deps Deps, fn handlerFn[I, O]) func(context.Context, I, dash.Principal) (O, error) {
	return runWith(deps, appRequired, fn)
}

// runPlatform is run for an intent that also accepts the empty (platform)
// scope, where sc.AppID is "".
func runPlatform[I, O any](deps Deps, fn handlerFn[I, O]) func(context.Context, I, dash.Principal) (O, error) {
	return runWith(deps, platformScope, fn)
}

// runWith is the one body behind both. Every store treats an empty app id as
// "all apps", so a handler that reached the engine with an empty scope would
// list other apps' rows. The check therefore sits here, before the handler
// runs, and not in each handler.
func runWith[I, O any](deps Deps, policy scopePolicy, fn handlerFn[I, O]) func(context.Context, I, dash.Principal) (O, error) {
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
		if sc.AppID == "" && policy == appRequired {
			return zero, &dash.Error{
				Code:    dash.CodePermissionDenied,
				Message: "no app selected: set the extension's app_id or send an app_id claim",
			}
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
	// platform lists the intents that accept the empty scope.
	platform map[string]bool
	err      error
}

func newBinder(d *dispatcher.Dispatcher, deps Deps) *binder {
	return &binder{d: d, deps: deps, kinds: map[string]string{}, platform: map[string]bool{}}
}

// query binds a read-only intent that needs a real app.
func query[I, O any](b *binder, name string, fn handlerFn[I, O]) {
	bindQuery(b, name, run(b.deps, fn))
}

// platformQuery binds a read-only intent that also accepts the empty scope.
func platformQuery[I, O any](b *binder, name string, fn handlerFn[I, O]) {
	bindQuery(b, name, runPlatform(b.deps, fn))
	b.platform[name] = true
}

// command binds a write intent that needs a real app.
func command[I, O any](b *binder, name string, fn handlerFn[I, O]) {
	bindCommand(b, name, run(b.deps, fn))
}

// platformCommand binds a write intent that also accepts the empty scope.
func platformCommand[I, O any](b *binder, name string, fn handlerFn[I, O]) {
	bindCommand(b, name, runPlatform(b.deps, fn))
	b.platform[name] = true
}

func bindQuery[I, O any](b *binder, name string, h func(context.Context, I, dash.Principal) (O, error)) {
	if b.err != nil {
		return
	}
	if err := dispatcher.RegisterQuery(b.d, contributorName, name, 1, h); err != nil {
		b.err = fmt.Errorf("ledger/contract: register %s: %w", name, err)
		return
	}
	b.kinds[name] = "query"
}

func bindCommand[I, O any](b *binder, name string, h func(context.Context, I, dash.Principal) (O, error)) {
	if b.err != nil {
		return
	}
	if err := dispatcher.RegisterCommand(b.d, contributorName, name, 1, h); err != nil {
		b.err = fmt.Errorf("ledger/contract: register %s: %w", name, err)
		return
	}
	b.kinds[name] = "command"
}
