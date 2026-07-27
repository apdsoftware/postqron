package entitlements

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

type PaddleCatalogManifest struct {
	Version  string                  `json:"version"`
	Currency string                  `json:"currency"`
	TaxMode  string                  `json:"tax_mode"`
	Products []PaddleProductManifest `json:"products"`
}

type PaddleProductManifest struct {
	Plan        PlanCode              `json:"plan"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	TaxCategory string                `json:"tax_category"`
	Prices      []PaddlePriceManifest `json:"prices"`
}

type PaddlePriceManifest struct {
	Interval        BillingInterval `json:"interval"`
	Tier            PriceTierCode   `json:"tier"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	UnitAmountCents int64           `json:"unit_amount_cents"`
	QuantityMinimum int64           `json:"quantity_minimum"`
	QuantityMaximum int64           `json:"quantity_maximum"`
}

func DecodePaddleCatalogManifest(reader io.Reader) (PaddleCatalogManifest, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	var manifest PaddleCatalogManifest
	if err := decoder.Decode(&manifest); err != nil {
		return PaddleCatalogManifest{}, fmt.Errorf("decode Paddle catalog manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return PaddleCatalogManifest{}, fmt.Errorf("decode Paddle catalog manifest: trailing data")
	}
	if err := manifest.Validate(); err != nil {
		return PaddleCatalogManifest{}, err
	}
	return manifest, nil
}

func (manifest PaddleCatalogManifest) Validate() error {
	if manifest.Version != PaddleCatalogVersion {
		return fmt.Errorf(
			"%w: manifest version must be %s",
			ErrCatalogMismatch,
			PaddleCatalogVersion,
		)
	}
	if manifest.Currency != "EUR" {
		return fmt.Errorf("%w: manifest currency must be EUR", ErrCatalogMismatch)
	}
	if manifest.TaxMode != "location" {
		return fmt.Errorf("%w: manifest tax mode must be location", ErrCatalogMismatch)
	}
	if len(manifest.Products) != 3 {
		return fmt.Errorf("%w: manifest must contain three paid products", ErrCatalogMismatch)
	}
	seenPlans := make(map[PlanCode]struct{}, len(manifest.Products))
	for _, product := range manifest.Products {
		if _, duplicate := seenPlans[product.Plan]; duplicate {
			return fmt.Errorf("%w: duplicate product for %s", ErrCatalogMismatch, product.Plan)
		}
		seenPlans[product.Plan] = struct{}{}
		publicPlan, err := PublicPlanByCode(product.Plan)
		if err != nil || !publicPlan.Purchasable {
			return fmt.Errorf("%w: invalid paid product %s", ErrCatalogMismatch, product.Plan)
		}
		if product.Name != "Postqron "+publicPlan.Name ||
			strings.TrimSpace(product.Description) == "" ||
			product.TaxCategory != "saas" {
			return fmt.Errorf("%w: invalid product metadata for %s", ErrCatalogMismatch, product.Plan)
		}
		if err := validateManifestPrices(product, publicPlan); err != nil {
			return err
		}
	}
	for _, plan := range []PlanCode{PlanPro, PlanTeam, PlanUnlimited} {
		if _, exists := seenPlans[plan]; !exists {
			return fmt.Errorf("%w: missing product for %s", ErrCatalogMismatch, plan)
		}
	}
	return nil
}

func validateManifestPrices(product PaddleProductManifest, publicPlan PublicPlan) error {
	tiers := []PriceTierCode{TierOne, TierTwo, TierThree}
	if product.Plan == PlanUnlimited {
		tiers = []PriceTierCode{TierFlat}
	}
	expected := len(tiers) * 2
	if len(product.Prices) != expected {
		return fmt.Errorf(
			"%w: product %s must contain %d prices",
			ErrCatalogMismatch,
			product.Plan,
			expected,
		)
	}
	seen := make(map[PaddlePriceKey]struct{}, expected)
	for _, price := range product.Prices {
		key := PaddlePriceKey{
			Plan: product.Plan, Interval: price.Interval, Tier: price.Tier,
		}
		if !validInterval(price.Interval) || !slices.Contains(tiers, price.Tier) {
			return fmt.Errorf("%w: invalid price key %v", ErrCatalogMismatch, key)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: duplicate price key %v", ErrCatalogMismatch, key)
		}
		seen[key] = struct{}{}
		if strings.TrimSpace(price.Name) == "" || strings.TrimSpace(price.Description) == "" {
			return fmt.Errorf("%w: missing price metadata for %v", ErrCatalogMismatch, key)
		}
		wantAmount, err := expectedPaddleAmount(publicPlan, price.Interval, price.Tier)
		if err != nil || price.UnitAmountCents != wantAmount {
			return fmt.Errorf("%w: price amount differs for %v", ErrCatalogMismatch, key)
		}
		wantMinimum, wantMaximum, err := ExpectedPaddleQuantity(key)
		if err != nil ||
			price.QuantityMinimum != wantMinimum ||
			price.QuantityMaximum != wantMaximum {
			return fmt.Errorf("%w: price quantity differs for %v", ErrCatalogMismatch, key)
		}
	}
	return nil
}

func expectedPaddleAmount(
	plan PublicPlan,
	interval BillingInterval,
	tier PriceTierCode,
) (int64, error) {
	if plan.Code == PlanUnlimited {
		if tier != TierFlat {
			return 0, ErrCatalogMismatch
		}
		if interval == IntervalAnnual {
			return plan.Prices.Annual.AmountCents, nil
		}
		return plan.Prices.Monthly.AmountCents, nil
	}
	tierIndex := slices.Index([]PriceTierCode{TierOne, TierTwo, TierThree}, tier)
	if tierIndex < 0 {
		return 0, ErrCatalogMismatch
	}
	if interval == IntervalAnnual {
		return plan.PriceTiers[tierIndex].Annual.AmountCents, nil
	}
	return plan.PriceTiers[tierIndex].Monthly.AmountCents, nil
}

func ExpectedPaddleQuantity(key PaddlePriceKey) (int64, int64, error) {
	switch key.Plan {
	case PlanPro, PlanTeam:
		switch key.Tier {
		case TierOne:
			plan, _ := PublicPlanByCode(key.Plan)
			return 1, *plan.Limits.Channels, nil
		case TierTwo:
			return 1, 15, nil
		case TierThree:
			return 1, 25, nil
		}
	case PlanUnlimited:
		if key.Tier == TierFlat {
			return 1, 1, nil
		}
	}
	return 0, 0, fmt.Errorf("%w: invalid price key %v", ErrCatalogMismatch, key)
}
