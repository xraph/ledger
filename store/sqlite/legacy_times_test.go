package sqlite_test

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"
	_ "github.com/xraph/grove/drivers/sqlitedriver/sqlitemigrate"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/sqlite"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// legacyNow is the clock every test here reads: noon UTC.
var legacyNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

var utcForm = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d+)? \+0000 UTC$`)

// legacySubs and legacyInvoices hold the text a release before edc6224, or a
// hand-written insert, left on disk. Each value is quoted as the instant it
// stands for, against legacyNow.
//
//	"2026-10-04 06:00:00 -0500 CDT"   11:00Z, an hour ago
//	"2026-10-04 08:00:00 -0500 CDT"   13:00Z, in an hour
//	"2026-10-04 12:30:00 +0200 +0200" 10:30Z, an hour and a half ago
//
// As text the first two sort before "2026-10-04 12:00:00 +0000 UTC" and the
// third after it, so a comparison against the clock gets the second and third
// wrong.
type legacySub struct{ name, start, end, cancelAt string }

var legacySubs = []legacySub{
	{"dueCDT", "2026-09-04 06:00:00 -0500 CDT", "2026-10-04 06:00:00 -0500 CDT", ""},
	{"futureCDT", "2026-09-04 08:00:00 -0500 CDT", "2026-10-04 08:00:00 -0500 CDT", ""},
	{"duePlus2", "2026-09-04 12:30:00 +0200 +0200", "2026-10-04 12:30:00 +0200 +0200", ""},
	{"dueMono", "2026-09-04 05:30:00.5 -0500 CDT m=-2595600.25", "2026-10-04 05:30:00.5 -0500 CDT m=-3600.25", ""},
	{"dueDefault", "2026-09-04 11:00:00", "2026-10-04 11:00:00", ""},
	{"cancelling", "2026-09-04 12:00:00 +0000 UTC", "2026-11-04 12:00:00 +0000 UTC", "2026-10-04 06:00:00 -0500 CDT"},
}

type legacyInvoice struct{ name, due string }

var legacyInvoices = []legacyInvoice{
	{"overdueCDT", "2026-10-04 06:00:00 -0500 CDT"},
	{"notYetCDT", "2026-10-04 08:00:00 -0500 CDT"},
	{"overduePlus2", "2026-10-04 12:30:00 +0200 +0200"},
}

// legacyDB is a migrated store holding the rows above, written as raw text
// the way an old release left them, and the Ledger names for each.
type legacyDB struct {
	t        *testing.T
	store    *sqlite.Store
	sdb      *sqlitedriver.SqliteDB
	plan     *plan.Plan
	subs     map[string]id.SubscriptionID
	invoices map[string]id.InvoiceID
}

func newLegacyDB(t *testing.T) *legacyDB {
	t.Helper()
	ctx := context.Background()

	sdb := sqlitedriver.New()
	if err := sdb.Open(ctx, "file:"+filepath.Join(t.TempDir(), "legacy.db")+"?_pragma=busy_timeout(5000)"); err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	db, err := grove.Open(sdb)
	if err != nil {
		t.Fatalf("grove.Open: %v", err)
	}
	s := sqlite.New(db)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	l := ledger.New(s)
	p := &plan.Plan{
		Name: "pro", Slug: "pro", Currency: "usd", AppID: "app_1",
		Pricing: &plan.Pricing{BaseAmount: types.USD(4900), BillingPeriod: plan.PeriodMonthly},
	}
	if err := l.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := l.ActivatePlan(ctx, p.ID); err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}

	d := &legacyDB{
		t: t, store: s, sdb: sdb, plan: p,
		subs: map[string]id.SubscriptionID{}, invoices: map[string]id.InvoiceID{},
	}
	for _, r := range legacySubs {
		sid := id.NewSubscriptionID()
		d.subs[r.name] = sid
		var cancel any
		if r.cancelAt != "" {
			cancel = r.cancelAt
		}
		d.exec(`INSERT INTO ledger_subscriptions
			(id, tenant_id, plan_id, status, current_period_start, current_period_end, cancel_at, app_id, created_at, updated_at)
			VALUES (?, ?, ?, 'active', ?, ?, ?, 'app_1', ?, ?)`,
			sid.String(), "t_"+r.name, p.ID.String(), r.start, r.end, cancel, r.start, r.start)
	}
	for _, r := range legacyInvoices {
		iid := id.NewInvoiceID()
		d.invoices[r.name] = iid
		d.exec(`INSERT INTO ledger_invoices
			(id, tenant_id, subscription_id, status, period_start, period_end, due_date, app_id, created_at, updated_at)
			VALUES (?, ?, ?, 'pending', ?, ?, ?, 'app_1', ?, ?)`,
			iid.String(), "t_"+r.name, d.subs["dueCDT"].String(), r.due, r.due, r.due, r.due, r.due)
	}
	return d
}

func (d *legacyDB) exec(query string, args ...any) {
	d.t.Helper()
	if _, err := d.sdb.Exec(context.Background(), query, args...); err != nil {
		d.t.Fatalf("exec %q: %v", query, err)
	}
}

// upgrade is what an operator does by deploying this release over a database
// an older one wrote: the normalize_time_text migration has not run yet, so
// forget that it has and migrate.
func (d *legacyDB) upgrade() {
	d.t.Helper()
	d.exec(`DELETE FROM grove_migrations WHERE version = '20240101000013'`)
	if err := d.store.Migrate(context.Background()); err != nil {
		d.t.Fatalf("Migrate over legacy rows: %v", err)
	}
}

func (d *legacyDB) text(table, column, key string) string {
	d.t.Helper()
	var out string
	row := d.sdb.QueryRow(context.Background(), "SELECT CAST("+column+" AS TEXT) FROM "+table+" WHERE id = ?", key)
	if err := row.Scan(&out); err != nil {
		d.t.Fatalf("read %s.%s: %v", table, column, err)
	}
	return out
}

func (d *legacyDB) subName(sid id.SubscriptionID) string {
	for n, v := range d.subs {
		if v == sid {
			return n
		}
	}
	return sid.String()
}

func (d *legacyDB) subNames(ids []id.SubscriptionID) []string {
	out := make([]string, 0, len(ids))
	for _, i := range ids {
		out = append(out, d.subName(i))
	}
	sort.Strings(out)
	return out
}

func (d *legacyDB) invoiceNames(ids []id.InvoiceID) []string {
	var out []string
	for _, i := range ids {
		for n, v := range d.invoices {
			if v == i {
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

func same(t *testing.T, what string, got, want []string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", what, got, want)
		}
	}
}

// The migration leaves every time in the UTC form and moves none of them.
func TestLegacyTimeTextIsRewrittenToUTC(t *testing.T) {
	d := newLegacyDB(t)
	for _, r := range legacySubs {
		if utcForm.MatchString(d.text("ledger_subscriptions", "current_period_end", d.subs[r.name].String())) && r.name != "cancelling" {
			t.Fatalf("%s: the seeded text is already in the UTC form, so the test seeds nothing legacy", r.name)
		}
	}

	d.upgrade()

	for _, r := range legacySubs {
		sid := d.subs[r.name].String()
		for column, was := range map[string]string{"current_period_start": r.start, "current_period_end": r.end} {
			got := d.text("ledger_subscriptions", column, sid)
			if !utcForm.MatchString(got) {
				t.Errorf("%s.%s = %q, want the UTC form", r.name, column, got)
			}
			if gotT, wantT := parse(t, got), parseLegacy(t, was); !gotT.Equal(wantT) {
				t.Errorf("%s.%s moved from %v to %v", r.name, column, wantT, gotT)
			}
		}
	}
	for _, r := range legacyInvoices {
		got := d.text("ledger_invoices", "due_date", d.invoices[r.name].String())
		if !utcForm.MatchString(got) {
			t.Errorf("%s.due_date = %q, want the UTC form", r.name, got)
		}
		if gotT, wantT := parse(t, got), parseLegacy(t, r.due); !gotT.Equal(wantT) {
			t.Errorf("%s.due_date moved from %v to %v", r.name, wantT, gotT)
		}
	}
	if got := d.text("ledger_subscriptions", "cancel_at", d.subs["cancelling"].String()); got != "2026-10-04 11:00:00 +0000 UTC" {
		t.Errorf("cancelling.cancel_at = %q, want 2026-10-04 11:00:00 +0000 UTC", got)
	}
	// A second pass finds nothing to rewrite.
	before := d.text("ledger_subscriptions", "current_period_end", d.subs["dueMono"].String())
	d.upgrade()
	if after := d.text("ledger_subscriptions", "current_period_end", d.subs["dueMono"].String()); after != before {
		t.Errorf("a second run changed %q to %q", before, after)
	}
}

// Text it cannot read stays as it was: Ledger could not read it before either,
// and a guess would turn into a billing date.
func TestLegacyTimeTextLeavesTheUnreadableAlone(t *testing.T) {
	d := newLegacyDB(t)
	sid := d.subs["dueCDT"].String()
	d.exec(`UPDATE ledger_subscriptions SET cancel_at = 'next tuesday' WHERE id = ?`, sid)
	d.upgrade()
	if got := d.text("ledger_subscriptions", "cancel_at", sid); got != "next tuesday" {
		t.Errorf("cancel_at = %q, want it left as it was", got)
	}
	if got := d.text("ledger_subscriptions", "current_period_end", sid); !utcForm.MatchString(got) {
		t.Errorf("current_period_end = %q, want the readable column rewritten anyway", got)
	}
}

// The due queries, the advance pinned on the end it listed, and the cancel
// that copies cancel_at from current_period_end, on rows an old release wrote.
func TestLegacyTimeTextStoreQueries(t *testing.T) {
	ctx := context.Background()
	d := newLegacyDB(t)
	d.upgrade()
	running := []subscription.Status{subscription.StatusActive, subscription.StatusTrialing, subscription.StatusPastDue}

	due, err := d.store.ListDueSubscriptions(ctx, subscription.DueOpts{Field: subscription.DuePeriodEnd, Before: legacyNow, Statuses: running})
	if err != nil {
		t.Fatalf("ListDueSubscriptions: %v", err)
	}
	got := make([]string, 0, len(due))
	for _, s := range due {
		got = append(got, d.subName(s.ID))
	}
	same(t, "periods due at noon", got,
		[]string{"dueCDT", "duePlus2", "dueMono", "dueDefault"})

	canceling, err := d.store.ListDueSubscriptions(ctx, subscription.DueOpts{Field: subscription.DueCancel, Before: legacyNow, Statuses: running})
	if err != nil {
		t.Fatalf("ListDueSubscriptions(cancel): %v", err)
	}
	if len(canceling) != 1 || d.subName(canceling[0].ID) != "cancelling" {
		t.Fatalf("cancels due at noon: got %d rows, want only cancelling", len(canceling))
	}

	overdue, err := d.store.ListOverdueInvoices(ctx, invoice.OverdueOpts{Before: legacyNow})
	if err != nil {
		t.Fatalf("ListOverdueInvoices: %v", err)
	}
	gotInv := make([]string, 0, len(overdue))
	for _, inv := range overdue {
		gotInv = append(gotInv, d.invoiceNames([]id.InvoiceID{inv.ID})...)
	}
	same(t, "invoices overdue at noon", gotInv, []string{"overdueCDT", "overduePlus2"})

	// The advance pins on the end it read back, which the old text no longer
	// has to match byte for byte.
	for _, name := range []string{"dueCDT", "duePlus2", "dueMono", "dueDefault"} {
		sub, getErr := d.store.GetSubscription(ctx, d.subs[name])
		if getErr != nil {
			t.Fatalf("GetSubscription(%s): %v", name, getErr)
		}
		from := sub.CurrentPeriodEnd
		next := from.AddDate(0, 1, 0)
		moved, advErr := d.store.AdvanceSubscriptionPeriod(ctx, sub.ID, from, from, next, legacyNow)
		if advErr != nil || !moved {
			t.Fatalf("AdvanceSubscriptionPeriod(%s): moved=%v err=%v, want it moved", name, moved, advErr)
		}
		again, advErr := d.store.AdvanceSubscriptionPeriod(ctx, sub.ID, from, from, next, legacyNow)
		if advErr != nil || again {
			t.Fatalf("AdvanceSubscriptionPeriod(%s) twice: moved=%v err=%v, want the second pinned out", name, again, advErr)
		}
	}
	// futureCDT ends at 13:00Z. At noon it must not move.
	future, err := d.store.GetSubscription(ctx, d.subs["futureCDT"])
	if err != nil {
		t.Fatalf("GetSubscription(futureCDT): %v", err)
	}
	if moved, mvErr := d.store.AdvanceSubscriptionPeriod(ctx, future.ID, future.CurrentPeriodEnd, future.CurrentPeriodEnd, future.CurrentPeriodEnd.AddDate(0, 1, 0), legacyNow); mvErr != nil || moved {
		t.Fatalf("AdvanceSubscriptionPeriod(futureCDT) at noon: moved=%v err=%v, want it left alone", moved, mvErr)
	}

	// A cancel at period end copies current_period_end into cancel_at inside
	// SQL. Enacted at noon it must wait for 13:00Z, and then go.
	at, err := d.store.CancelSubscription(ctx, d.subs["futureCDT"], false)
	if err != nil {
		t.Fatalf("CancelSubscription(futureCDT): %v", err)
	}
	if want := time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC); !at.Equal(want) {
		t.Fatalf("CancelSubscription(futureCDT) cancel_at = %v, want %v", at, want)
	}
	if enacted, err := d.store.EnactSubscriptionCancel(ctx, d.subs["futureCDT"], legacyNow); err != nil || enacted {
		t.Fatalf("EnactSubscriptionCancel(futureCDT) at noon: enacted=%v err=%v, want it to wait", enacted, err)
	}
	if enacted, err := d.store.EnactSubscriptionCancel(ctx, d.subs["futureCDT"], legacyNow.Add(time.Hour)); err != nil || !enacted {
		t.Fatalf("EnactSubscriptionCancel(futureCDT) at 13:00Z: enacted=%v err=%v, want it enacted", enacted, err)
	}
}

// The clock end to end: one Advance over a legacy database moves what is due,
// cancels what is due, marks what is overdue, touches nothing else, and has
// nothing left to do on the next tick.
func TestLegacyTimeTextClock(t *testing.T) {
	ctx := context.Background()
	d := newLegacyDB(t)
	d.upgrade()
	l := ledger.New(d.store)

	report, err := l.Advance(ctx, legacyNow)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	same(t, "periods advanced", d.subNames(report.PeriodsAdvanced),
		[]string{"dueCDT", "duePlus2", "dueMono", "dueDefault"})
	same(t, "cancels enacted", d.subNames(report.CancelsEnacted), []string{"cancelling"})
	same(t, "invoices past due", d.invoiceNames(report.InvoicesPastDue), []string{"overdueCDT", "overduePlus2"})

	next, err := l.Advance(ctx, legacyNow)
	if err != nil {
		t.Fatalf("second Advance: %v", err)
	}
	if !next.Empty() {
		t.Fatalf("second Advance did work again: %+v", next)
	}

	// An hour and a minute later futureCDT and notYetCDT are due.
	later, err := l.Advance(ctx, legacyNow.Add(time.Hour+time.Minute))
	if err != nil {
		t.Fatalf("Advance an hour later: %v", err)
	}
	same(t, "periods advanced an hour later", d.subNames(later.PeriodsAdvanced), []string{"futureCDT"})
	same(t, "invoices past due an hour later", d.invoiceNames(later.InvoicesPastDue), []string{"notYetCDT"})
}

func parse(t *testing.T, text string) time.Time {
	t.Helper()
	v, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return v
}

// parseLegacy reads the seeded text the same way the migration should.
func parseLegacy(t *testing.T, text string) time.Time {
	t.Helper()
	instants := map[string]time.Time{
		"2026-09-04 06:00:00 -0500 CDT":                 time.Date(2026, 9, 4, 11, 0, 0, 0, time.UTC),
		"2026-10-04 06:00:00 -0500 CDT":                 time.Date(2026, 10, 4, 11, 0, 0, 0, time.UTC),
		"2026-09-04 08:00:00 -0500 CDT":                 time.Date(2026, 9, 4, 13, 0, 0, 0, time.UTC),
		"2026-10-04 08:00:00 -0500 CDT":                 time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC),
		"2026-09-04 12:30:00 +0200 +0200":               time.Date(2026, 9, 4, 10, 30, 0, 0, time.UTC),
		"2026-10-04 12:30:00 +0200 +0200":               time.Date(2026, 10, 4, 10, 30, 0, 0, time.UTC),
		"2026-09-04 05:30:00.5 -0500 CDT m=-2595600.25": time.Date(2026, 9, 4, 10, 30, 0, 500_000_000, time.UTC),
		"2026-10-04 05:30:00.5 -0500 CDT m=-3600.25":    time.Date(2026, 10, 4, 10, 30, 0, 500_000_000, time.UTC),
		"2026-09-04 11:00:00":                           time.Date(2026, 9, 4, 11, 0, 0, 0, time.UTC),
		"2026-10-04 11:00:00":                           time.Date(2026, 10, 4, 11, 0, 0, 0, time.UTC),
		"2026-09-04 12:00:00 +0000 UTC":                 time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
		"2026-11-04 12:00:00 +0000 UTC":                 time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC),
	}
	v, ok := instants[text]
	if !ok {
		t.Fatalf("no instant listed for seeded text %q", text)
	}
	return v
}
