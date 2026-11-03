package ledger

import (
	"fmt"
	"strings"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/types"
)

// creditLines turns each of the plan's credit pools into one negative credit
// line, and returns the lines with their total as a positive amount.
//
// A pool's credit is the smaller of the pool's amount and the metered
// charges already on the invoice for the pool's features, so a pool can only
// cancel usage. It never reaches the base fee or the seat charges, and it
// never goes negative. A pool with no usage to cancel adds no line at all.
// The returned total is always in the given currency, which is the plan's.
func creditLines(p *plan.Plan, items []invoice.LineItem, invID id.InvoiceID, currency string) ([]invoice.LineItem, types.Money, error) {
	total := types.Zero(currency)
	var lines []invoice.LineItem

	for _, pool := range p.Credits {
		if pc := strings.ToLower(pool.Amount.Currency); pc != currency {
			return nil, types.Money{}, fmt.Errorf("credit pool %q is in %q, plan bills in %q", pool.Key, pc, currency)
		}

		inPool := make(map[string]bool, len(pool.FeatureKeys))
		for _, k := range pool.FeatureKeys {
			inPool[k] = true
		}

		used := types.Zero(currency)
		for _, li := range items {
			if li.Type != invoice.LineItemOverage && li.Type != invoice.LineItemUsage {
				continue
			}
			if !inPool[li.FeatureKey] {
				continue
			}
			sum, err := addChecked(fmt.Sprintf("credit pool %q: adding feature %q usage", pool.Key, li.FeatureKey), used, li.Amount)
			if err != nil {
				return nil, types.Money{}, err
			}
			used = sum
		}

		credit := used
		if pool.Amount.Amount < credit.Amount {
			credit = types.Money{Amount: pool.Amount.Amount, Currency: currency}
		}
		if !credit.IsPositive() {
			continue
		}

		lines = append(lines, invoice.LineItem{
			ID:          id.NewLineItemID(),
			InvoiceID:   invID,
			Description: pool.Key + " monthly credit",
			Quantity:    1,
			UnitAmount:  credit.Negate(),
			Amount:      credit.Negate(),
			Type:        invoice.LineItemCredit,
			Metadata:    map[string]string{"credit_pool": pool.Key},
		})
		sum, err := addChecked(fmt.Sprintf("credit: adding pool %q", pool.Key), total, credit)
		if err != nil {
			return nil, types.Money{}, err
		}
		total = sum
	}

	return lines, total, nil
}
