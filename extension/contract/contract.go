// Package contract wires Ledger into the Forge dashboard's contract path. It
// declares the `ledger` contributor and its intents, and binds the typed
// handlers that answer them. The React plugin in forge-dashboard
// (packages/plugin-ledger) reads these intents. The templ contributor in
// ../../dashboard is separate, and is retired on its own schedule.
package contract

import (
	"bytes"
	_ "embed"
	"fmt"

	dash "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"
	"github.com/xraph/forge/extensions/dashboard/contract/loader"

	ledger "github.com/xraph/ledger"
)

//go:embed manifest.yaml
var manifestYAML []byte

const contributorName = "ledger"

// SettingsView is the read-only engine configuration settings.detail reports.
// It comes from the extension's resolved configuration, not from constants.
type SettingsView struct {
	MeterBatchSize      int    `json:"meter_batch_size"`
	MeterFlushInterval  string `json:"meter_flush_interval"`
	EntitlementCacheTTL string `json:"entitlement_cache_ttl"`
}

// Deps is everything the handlers need. Engine is a function because the
// dashboard registers contributors before every extension has finished
// starting; a handler that runs before the engine exists answers UNAVAILABLE.
type Deps struct {
	Engine func() *ledger.Ledger

	// AppID is the extension's configured app, used when the principal carries
	// no app_id claim. Empty in a single-app deployment.
	AppID string

	// RequireAppClaim refuses any request whose principal has no app_id claim,
	// even when AppID is set. Turn it on in a multi-app deployment, where an
	// unscoped read would return every app's rows.
	RequireAppClaim bool

	Settings func() SettingsView
}

func (d Deps) engine() *ledger.Ledger {
	if d.Engine == nil {
		return nil
	}
	return d.Engine()
}

// Register loads and validates the manifest, registers the contributor, and
// binds every intent. The dashboard calls it through
// Extension.RegisterContractContributor.
func Register(d *dispatcher.Dispatcher, reg dash.Registry, wreg dash.WardenRegistry, deps Deps) error {
	if deps.Engine == nil {
		return fmt.Errorf("ledger/contract: Engine is required")
	}

	m, err := loadManifest()
	if err != nil {
		return err
	}
	if err := loader.Validate(m, wreg); err != nil {
		return fmt.Errorf("ledger/contract: validate manifest: %w", err)
	}
	if err := reg.Register(m); err != nil {
		return fmt.Errorf("ledger/contract: register manifest: %w", err)
	}

	b := newBinder(d, deps)
	registerAll(b)

	return b.err
}

func loadManifest() (*dash.ContractManifest, error) {
	m, err := loader.Load(bytes.NewReader(manifestYAML), "ledger/contract/manifest.yaml")
	if err != nil {
		return nil, fmt.Errorf("ledger/contract: load manifest: %w", err)
	}
	return m, nil
}

func validateManifest(m *dash.ContractManifest) error {
	return loader.Validate(m, dash.NewWardenRegistry())
}

// registerAll binds every surface. Each surface's task adds one line here.
func registerAll(b *binder) {
	_ = b
}

// Ack is the answer to a command with nothing else to report.
type Ack struct {
	OK bool `json:"ok"`
}
