package contract

import (
	"sort"
	"strings"
	"testing"

	dash "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"

	ledger "github.com/xraph/ledger"
)

const wantIntents = 47

// Every intent the manifest declares is bound with its declared kind, nothing
// is bound that is not declared, and every invalidates entry names a declared
// query. Because every binding goes through the binder, which wraps run, this
// also proves every handler resolves the app scope.
func TestManifestAndRegistrationsAgree(t *testing.T) {
	m, err := loadManifest()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	b := newBinder(dispatcher.New(nil), Deps{Engine: func() *ledger.Ledger { return nil }})
	registerAll(b)
	if b.err != nil {
		t.Fatalf("registerAll: %v", b.err)
	}

	declared := map[string]string{}
	for _, in := range m.Intents {
		declared[in.Name] = string(in.Kind)
	}
	if len(declared) != wantIntents {
		t.Errorf("manifest declares %d intents, want %d", len(declared), wantIntents)
	}
	for name, kind := range declared {
		if b.kinds[name] != kind {
			t.Errorf("%s: declared %q, bound as %q", name, kind, b.kinds[name])
		}
	}
	for name := range b.kinds {
		if _, ok := declared[name]; !ok {
			t.Errorf("%s is bound but not declared", name)
		}
	}
	for _, in := range m.Intents {
		switch in.Kind {
		case dash.IntentKindCommand:
			if len(in.Invalidates) == 0 {
				t.Errorf("command %s invalidates nothing", in.Name)
			}
			for _, q := range in.Invalidates {
				if declared[q] != string(dash.IntentKindQuery) {
					t.Errorf("command %s invalidates %q, which is not a declared query", in.Name, q)
				}
			}
		case dash.IntentKindQuery:
			if len(in.Invalidates) != 0 {
				t.Errorf("query %s declares invalidates", in.Name)
			}
		}
	}
}

// The intents that may run without an app are the seven feature catalog
// intents, whose global rows belong to no app, and settings.detail, which
// reports configuration and reads no app data. Every other intent refuses the
// empty scope.
func TestPlatformIntentsAreTheFeatureCatalogAndSettings(t *testing.T) {
	b := newBinder(dispatcher.New(nil), Deps{})
	registerAll(b)
	if b.err != nil {
		t.Fatalf("registerAll: %v", b.err)
	}

	var got []string
	for name := range b.platform {
		got = append(got, name)
	}
	var want []string
	for name := range b.kinds {
		if strings.HasPrefix(name, "features.") || name == "settings.detail" {
			want = append(want, name)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(want) != 8 {
		t.Fatalf("expected 7 features.* intents plus settings.detail, found %v", want)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("platform intents = %v, want exactly %v", got, want)
	}
}

func TestRegisterAgainstARealRegistry(t *testing.T) {
	reg := dash.NewRegistry()
	deps := Deps{Engine: func() *ledger.Ledger { return nil }}
	if err := Register(dispatcher.New(nil), reg, dash.NewWardenRegistry(), deps); err != nil {
		t.Fatalf("Register: %v", err)
	}
	m, ok := reg.Contributor(contributorName)
	if !ok {
		t.Fatal("the ledger contributor is not in the registry")
	}
	if len(m.Intents) != wantIntents {
		t.Errorf("registered %d intents, want %d", len(m.Intents), wantIntents)
	}
}

// The intents the spec deliberately dropped must stay dropped.
func TestDroppedIntentsAreAbsent(t *testing.T) {
	m, err := loadManifest()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	dropped := map[string]bool{
		"plans.importFromProvider": true, "features.importFromProvider": true,
		"subscriptions.importFromProvider": true, "invoices.importFromProvider": true,
		"usage.purge": true, "providers.list": true,
	}
	for _, in := range m.Intents {
		if dropped[in.Name] {
			t.Errorf("%s is declared; the spec drops it (see its Phase B corrections)", in.Name)
		}
	}
}
