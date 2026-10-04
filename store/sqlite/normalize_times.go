package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/xraph/grove/migrate"
)

// SQLite compares these timestamps as text, so every one has to be written in
// one form: the UTC form below. Ledger has written it since edc6224, and
// grove's sqlite driver does as well since v1.7.0. A row from before that can
// hold a local zone ("2026-10-04 09:00:00 -0500 CDT"), a nameless one
// ("... +0200 +0200"), a monotonic reading ("... -0500 CDT m=-5399.98") or
// the "YYYY-MM-DD HH:MM:SS" that a column default writes. Each of those reads
// back to the right instant and sorts by the wrong text. The migration below
// rewrites them once.

// timeTables lists every time column Ledger stores on sqlite, with the key of
// its table.
var timeTables = []struct {
	table, key string
	columns    []string
}{
	{"ledger_plans", "id", []string{"created_at", "updated_at"}},
	{"ledger_subscriptions", "id", []string{
		"current_period_start", "current_period_end", "trial_start", "trial_end",
		"canceled_at", "cancel_at", "ended_at", "paused_at",
		"stretch_start", "stretch_end", "stretch_original_end", "stretch_floor",
		"created_at", "updated_at",
	}},
	{"ledger_usage_events", "id", []string{"timestamp", "created_at"}},
	{"ledger_entitlement_cache", "cache_key", []string{"expires_at", "created_at"}},
	{"ledger_invoices", "id", []string{
		"period_start", "period_end", "due_date", "paid_at", "voided_at",
		"created_at", "updated_at",
	}},
	{"ledger_coupons", "id", []string{"valid_from", "valid_until", "created_at", "updated_at"}},
	{"ledger_features", "id", []string{"created_at", "updated_at"}},
	{"ledger_coupon_applications", "id", []string{"applied_at"}},
}

const (
	// normalizeBatch is how many rows one pass reads before it rewrites them.
	normalizeBatch = 500

	// canonicalGlob matches the UTC form: a date, a time, an optional
	// fraction, then " +0000 UTC".
	canonicalGlob = "[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9] [0-9][0-9]:[0-9][0-9]:[0-9][0-9]* +0000 UTC"
)

var canonicalTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d+)? \+0000 UTC$`)

// storedLayouts are the forms a time has been stored in, tried in order.
var storedLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999 -0700 -0700", // a zone with no name
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
	time.DateTime, // what a column default writes, in UTC
	time.DateOnly,
}

// parseStoredTime reads any form Ledger has stored a time in. It drops the
// " m=+..." monotonic reading first: that reading only means something to the
// process that took it.
func parseStoredTime(text string) (time.Time, bool) {
	if i := strings.LastIndex(text, " m="); i >= 0 && i+3 < len(text) && (text[i+3] == '+' || text[i+3] == '-') {
		text = text[:i]
	}
	for _, layout := range storedLayouts {
		if t, err := time.Parse(layout, text); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// normalizeTimeText rewrites every stored time that is not in the UTC form.
// It leaves a value it cannot parse alone, since it could not read it before
// either, and it changes a column only while that column still holds the text
// it read, so a row another writer touched meanwhile is not overwritten. It
// can run again at any point and finds nothing left to do.
func normalizeTimeText(ctx context.Context, exec migrate.Executor) error {
	for _, tbl := range timeTables {
		if err := normalizeTable(ctx, exec, tbl.table, tbl.key, tbl.columns); err != nil {
			return fmt.Errorf("normalize %s: %w", tbl.table, err)
		}
	}
	return nil
}

func normalizeTable(ctx context.Context, exec migrate.Executor, table, key string, columns []string) error {
	selects := make([]string, len(columns))
	stale := make([]string, len(columns))
	for i, c := range columns {
		selects[i] = "CAST(" + c + " AS TEXT)"
		stale[i] = "(" + c + " IS NOT NULL AND " + c + " NOT GLOB '" + canonicalGlob + "')"
	}
	query := "SELECT " + key + ", " + strings.Join(selects, ", ") +
		" FROM " + table +
		" WHERE " + key + " > ? AND (" + strings.Join(stale, " OR ") + ")" +
		" ORDER BY " + key + " LIMIT " + fmt.Sprint(normalizeBatch)

	last := ""
	for {
		type row struct {
			key  string
			vals []sql.NullString
		}
		var batch []row
		rows, err := exec.Query(ctx, query, last)
		if err != nil {
			return err
		}
		for rows.Next() {
			r := row{vals: make([]sql.NullString, len(columns))}
			dest := make([]any, 0, len(columns)+1)
			dest = append(dest, &r.key)
			for i := range r.vals {
				dest = append(dest, &r.vals[i])
			}
			if scanErr := rows.Scan(dest...); scanErr != nil {
				_ = rows.Close()
				return scanErr
			}
			batch = append(batch, r)
		}
		err = rows.Err()
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, r := range batch {
			var sets, guards []string
			var setArgs, guardArgs []any
			for i, v := range r.vals {
				if !v.Valid || canonicalTime.MatchString(v.String) {
					continue
				}
				t, ok := parseStoredTime(v.String)
				if !ok {
					continue
				}
				sets = append(sets, columns[i]+" = ?")
				setArgs = append(setArgs, t.UTC())
				guards = append(guards, "CAST("+columns[i]+" AS TEXT) = ?")
				guardArgs = append(guardArgs, v.String)
			}
			if len(sets) == 0 {
				continue
			}
			args := append(append(setArgs, r.key), guardArgs...)
			update := "UPDATE " + table + " SET " + strings.Join(sets, ", ") +
				" WHERE " + key + " = ? AND " + strings.Join(guards, " AND ")
			if _, err := exec.Exec(ctx, update, args...); err != nil {
				return err
			}
		}
		last = batch[len(batch)-1].key
	}
}
