// Package extension provides the Forge extension adapter for Ledger.
//
// It implements the forge.Extension interface to integrate Ledger
// into a Forge application with automatic dependency discovery,
// DI registration, and lifecycle management.
//
// Configuration can be provided programmatically via Option functions
// or via YAML configuration files under "extensions.ledger" or "ledger" keys.
package extension

import (
	"context"
	"errors"

	"github.com/xraph/forge"
	"github.com/xraph/vessel"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/store"
	"github.com/xraph/ledger/store/memory"
)

// ExtensionName is the name registered with Forge.
const ExtensionName = "ledger"

// ExtensionDescription is the human-readable description.
const ExtensionDescription = "Composable usage-based billing engine"

// ExtensionVersion is the semantic version.
const ExtensionVersion = "0.1.0"

// Ensure Extension implements forge.Extension at compile time.
var _ forge.Extension = (*Extension)(nil)

// Extension adapts Ledger as a Forge extension.
type Extension struct {
	*forge.BaseExtension

	config     Config
	engine     *ledger.Ledger
	store      store.Store
	ledgerOpts []ledger.Option
}

// New creates a new Ledger Forge extension with the given options.
func New(opts ...Option) *Extension {
	e := &Extension{
		BaseExtension: forge.NewBaseExtension(ExtensionName, ExtensionVersion, ExtensionDescription),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Engine returns the underlying Ledger instance.
// This is nil until Register is called.
func (e *Extension) Engine() *ledger.Ledger { return e.engine }

// Register implements [forge.Extension]. It loads configuration,
// initializes the ledger engine, and registers it in the DI container.
func (e *Extension) Register(fapp forge.App) error {
	if err := e.BaseExtension.Register(fapp); err != nil {
		return err
	}

	if err := e.loadConfiguration(); err != nil {
		return err
	}

	// Use memory store if no store was provided programmatically.
	if e.store == nil {
		e.store = memory.New()
	}

	// Build ledger options from resolved config.
	opts := e.buildLedgerOpts()

	eng := ledger.New(e.store, opts...)
	e.engine = eng

	return vessel.Provide(fapp.Container(), func() (*ledger.Ledger, error) {
		return e.engine, nil
	})
}

// Start implements [forge.Extension].
func (e *Extension) Start(ctx context.Context) error {
	if e.engine == nil {
		return errors.New("ledger: extension not initialized")
	}

	if !e.config.DisableMigrate {
		if err := e.engine.Start(ctx); err != nil {
			return err
		}
	}

	e.MarkStarted()
	return nil
}

// Stop implements [forge.Extension].
func (e *Extension) Stop(_ context.Context) error {
	if e.engine != nil {
		if err := e.engine.Stop(); err != nil {
			e.MarkStopped()
			return err
		}
	}
	e.MarkStopped()
	return nil
}

// Health implements [forge.Extension].
func (e *Extension) Health(ctx context.Context) error {
	if e.store == nil {
		return errors.New("ledger: store not initialized")
	}
	return e.store.Ping(ctx)
}

// buildLedgerOpts constructs ledger.Option values from the resolved config.
func (e *Extension) buildLedgerOpts() []ledger.Option {
	opts := make([]ledger.Option, 0, len(e.ledgerOpts)+3)

	// Apply config-derived options.
	if e.config.MeterBatchSize > 0 || e.config.MeterFlushInterval > 0 {
		batchSize := e.config.MeterBatchSize
		flushInterval := e.config.MeterFlushInterval
		defaults := DefaultConfig()
		if batchSize == 0 {
			batchSize = defaults.MeterBatchSize
		}
		if flushInterval == 0 {
			flushInterval = defaults.MeterFlushInterval
		}
		opts = append(opts, ledger.WithMeterConfig(batchSize, flushInterval))
	}

	if e.config.EntitlementCacheTTL > 0 {
		opts = append(opts, ledger.WithEntitlementCacheTTL(e.config.EntitlementCacheTTL))
	}

	// Append any pass-through ledger options.
	opts = append(opts, e.ledgerOpts...)

	return opts
}

// --- Config Loading (mirrors grove/shield extension pattern) ---

// loadConfiguration loads config from YAML files or programmatic sources.
func (e *Extension) loadConfiguration() error {
	programmaticConfig := e.config

	// Try loading from config file.
	fileConfig, configLoaded := e.tryLoadFromConfigFile()

	if !configLoaded {
		if programmaticConfig.RequireConfig {
			return errors.New("ledger: configuration is required but not found in config files; " +
				"ensure 'extensions.ledger' or 'ledger' key exists in your config")
		}

		// Use programmatic config merged with defaults.
		e.config = e.mergeWithDefaults(programmaticConfig)
	} else {
		// Config loaded from YAML -- merge with programmatic options.
		e.config = e.mergeConfigurations(fileConfig, programmaticConfig)
	}

	e.Logger().Debug("ledger: configuration loaded",
		forge.F("disable_routes", e.config.DisableRoutes),
		forge.F("disable_migrate", e.config.DisableMigrate),
		forge.F("base_path", e.config.BasePath),
		forge.F("meter_batch_size", e.config.MeterBatchSize),
		forge.F("meter_flush_interval", e.config.MeterFlushInterval),
		forge.F("entitlement_cache_ttl", e.config.EntitlementCacheTTL),
	)

	return nil
}

// tryLoadFromConfigFile attempts to load config from YAML files.
func (e *Extension) tryLoadFromConfigFile() (Config, bool) {
	cm := e.App().Config()
	var cfg Config

	// Try "extensions.ledger" first (namespaced pattern).
	if cm.IsSet("extensions.ledger") {
		if err := cm.Bind("extensions.ledger", &cfg); err == nil {
			e.Logger().Debug("ledger: loaded config from file",
				forge.F("key", "extensions.ledger"),
			)
			return cfg, true
		}
		e.Logger().Warn("ledger: failed to bind extensions.ledger config",
			forge.F("error", "bind failed"),
		)
	}

	// Try legacy "ledger" key.
	if cm.IsSet("ledger") {
		if err := cm.Bind("ledger", &cfg); err == nil {
			e.Logger().Debug("ledger: loaded config from file",
				forge.F("key", "ledger"),
			)
			return cfg, true
		}
		e.Logger().Warn("ledger: failed to bind ledger config",
			forge.F("error", "bind failed"),
		)
	}

	return Config{}, false
}

// mergeWithDefaults fills zero-valued fields with defaults.
func (e *Extension) mergeWithDefaults(cfg Config) Config {
	defaults := DefaultConfig()
	if cfg.MeterBatchSize == 0 {
		cfg.MeterBatchSize = defaults.MeterBatchSize
	}
	if cfg.MeterFlushInterval == 0 {
		cfg.MeterFlushInterval = defaults.MeterFlushInterval
	}
	if cfg.EntitlementCacheTTL == 0 {
		cfg.EntitlementCacheTTL = defaults.EntitlementCacheTTL
	}
	return cfg
}

// mergeConfigurations merges YAML config with programmatic options.
// YAML config takes precedence for most fields; programmatic bool flags fill gaps.
func (e *Extension) mergeConfigurations(yamlConfig, programmaticConfig Config) Config {
	// Programmatic bool flags override when true.
	if programmaticConfig.DisableRoutes {
		yamlConfig.DisableRoutes = true
	}
	if programmaticConfig.DisableMigrate {
		yamlConfig.DisableMigrate = true
	}

	// String fields: YAML takes precedence.
	if yamlConfig.BasePath == "" && programmaticConfig.BasePath != "" {
		yamlConfig.BasePath = programmaticConfig.BasePath
	}

	// Duration/int fields: YAML takes precedence, programmatic fills gaps.
	if yamlConfig.MeterBatchSize == 0 && programmaticConfig.MeterBatchSize != 0 {
		yamlConfig.MeterBatchSize = programmaticConfig.MeterBatchSize
	}
	if yamlConfig.MeterFlushInterval == 0 && programmaticConfig.MeterFlushInterval != 0 {
		yamlConfig.MeterFlushInterval = programmaticConfig.MeterFlushInterval
	}
	if yamlConfig.EntitlementCacheTTL == 0 && programmaticConfig.EntitlementCacheTTL != 0 {
		yamlConfig.EntitlementCacheTTL = programmaticConfig.EntitlementCacheTTL
	}

	// Fill remaining zeros with defaults.
	return e.mergeWithDefaults(yamlConfig)
}
