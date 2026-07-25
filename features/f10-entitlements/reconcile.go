package entitlements

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
)

type CatalogCheck struct {
	Key     PaddlePriceKey
	PriceID string
	OK      bool
	Reason  string
}

func (client *PaddleClient) DryRunCatalog(
	ctx context.Context,
	catalog PaddleCatalog,
) ([]CatalogCheck, error) {
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	keys := make([]PaddlePriceKey, 0, len(catalog))
	for key := range catalog {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(left, right PaddlePriceKey) int {
		leftValue := string(left.Plan) + "/" + string(left.Interval) + "/" + string(left.Tier)
		rightValue := string(right.Plan) + "/" + string(right.Interval) + "/" + string(right.Tier)
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
		return 0
	})
	checks := make([]CatalogCheck, 0, len(keys))
	for _, key := range keys {
		mapping := catalog[key]
		var response struct {
			Data struct {
				ID        string `json:"id"`
				ProductID string `json:"product_id"`
				Status    string `json:"status"`
				UnitPrice struct {
					Amount       string `json:"amount"`
					CurrencyCode string `json:"currency_code"`
				} `json:"unit_price"`
				BillingCycle *struct {
					Interval  string `json:"interval"`
					Frequency int64  `json:"frequency"`
				} `json:"billing_cycle"`
			} `json:"data"`
		}
		err := client.doJSON(
			ctx,
			http.MethodGet,
			"/prices/"+url.PathEscape(mapping.PriceID),
			nil,
			&response,
		)
		check := CatalogCheck{Key: key, PriceID: mapping.PriceID}
		if err != nil {
			check.Reason = err.Error()
			checks = append(checks, check)
			continue
		}
		interval := "month"
		if key.Interval == IntervalAnnual {
			interval = "year"
		}
		wantAmount := fmt.Sprintf("%d", mapping.UnitAmountCents)
		switch {
		case response.Data.ID != mapping.PriceID:
			check.Reason = "price ID differs"
		case response.Data.ProductID != mapping.ProductID:
			check.Reason = "product ID differs"
		case response.Data.Status != "active":
			check.Reason = "price is not active"
		case response.Data.UnitPrice.Amount != wantAmount:
			check.Reason = "unit amount differs"
		case response.Data.UnitPrice.CurrencyCode != "EUR":
			check.Reason = "base currency is not EUR"
		case response.Data.BillingCycle == nil ||
			response.Data.BillingCycle.Interval != interval ||
			response.Data.BillingCycle.Frequency != 1:
			check.Reason = "billing interval differs"
		default:
			check.OK = true
		}
		checks = append(checks, check)
	}
	return checks, nil
}

func WriteCatalogDryRun(writer io.Writer, checks []CatalogCheck) error {
	failed := 0
	for _, check := range checks {
		status := "ok"
		if !check.OK {
			status = "mismatch: " + check.Reason
			failed++
		}
		if _, err := fmt.Fprintf(
			writer,
			"%s/%s/%s %s %s\n",
			check.Key.Plan,
			check.Key.Interval,
			check.Key.Tier,
			check.PriceID,
			status,
		); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%w: %d price mappings differ", ErrCatalogMismatch, failed)
	}
	return nil
}
