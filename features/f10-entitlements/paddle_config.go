package entitlements

import (
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

type PaddleEnvironment string

const (
	PaddleSandbox    PaddleEnvironment = "sandbox"
	PaddleProduction PaddleEnvironment = "production"
)

type PriceTierCode string

const (
	TierOne   PriceTierCode = "1-10"
	TierTwo   PriceTierCode = "11-25"
	TierThree PriceTierCode = "26-50"
	TierFlat  PriceTierCode = "flat"
)

type PaddlePriceKey struct {
	Plan     PlanCode
	Interval BillingInterval
	Tier     PriceTierCode
}

type PaddlePriceMapping struct {
	Plan            PlanCode        `json:"plan"`
	Interval        BillingInterval `json:"interval"`
	Tier            PriceTierCode   `json:"tier"`
	ProductID       string          `json:"product_id"`
	PriceID         string          `json:"price_id"`
	UnitAmountCents int64           `json:"unit_amount_cents"`
}

type PaddleCatalog map[PaddlePriceKey]PaddlePriceMapping

type PaddleItem struct {
	PriceID  string `json:"price_id"`
	Quantity int64  `json:"quantity"`
}

type PaddleConfig struct {
	Environment   PaddleEnvironment
	APIKey        string
	WebhookSecret string
	Catalog       PaddleCatalog
}

var (
	paddleIDPattern     = regexp.MustCompile(`^(pro|pri)_[a-z\d]{26}$`)
	paddleAPIKeyPattern = regexp.MustCompile(
		`^pdl_(live|sdbx)_apikey_[a-z\d]{26}_[a-zA-Z\d]{22}_[a-zA-Z\d]{3}$`,
	)
	ErrInvalidPaddleConfig = errors.New("invalid Paddle configuration")
	ErrMissingPaddlePrice  = errors.New("Paddle price is not configured")
	ErrCatalogMismatch     = errors.New("Paddle catalog does not match D09")
)

func (config PaddleConfig) Validate() error {
	if err := validatePaddleEnvironmentAndKey(config.Environment, config.APIKey); err != nil {
		return err
	}
	if strings.TrimSpace(config.WebhookSecret) == "" {
		return fmt.Errorf("%w: webhook secret is required", ErrInvalidPaddleConfig)
	}
	if err := config.Catalog.Validate(); err != nil {
		return err
	}
	return nil
}

func validatePaddleEnvironmentAndKey(environment PaddleEnvironment, apiKey string) error {
	if environment != PaddleSandbox && environment != PaddleProduction {
		return fmt.Errorf("%w: environment must be sandbox or production", ErrInvalidPaddleConfig)
	}
	match := paddleAPIKeyPattern.FindStringSubmatch(apiKey)
	if len(match) != 2 {
		return fmt.Errorf("%w: current-format API key is required", ErrInvalidPaddleConfig)
	}
	if (environment == PaddleSandbox && match[1] != "sdbx") ||
		(environment == PaddleProduction && match[1] != "live") {
		return fmt.Errorf("%w: API key environment mismatch", ErrInvalidPaddleConfig)
	}
	return nil
}

func NewPaddleConfigFromEnv() (PaddleConfig, error) {
	return NewPaddleConfig(
		os.Getenv("PADDLE_ENVIRONMENT"),
		os.Getenv("PADDLE_API_KEY"),
		os.Getenv("PADDLE_WEBHOOK_SECRET"),
		os.Getenv("PADDLE_CATALOG_JSON"),
	)
}

func NewPaddleConfig(
	environment string,
	apiKey string,
	webhookSecret string,
	catalogJSON string,
) (PaddleConfig, error) {
	var mappings []PaddlePriceMapping
	if err := json.Unmarshal([]byte(catalogJSON), &mappings); err != nil {
		return PaddleConfig{}, fmt.Errorf("%w: decode PADDLE_CATALOG_JSON", ErrInvalidPaddleConfig)
	}
	catalog := make(PaddleCatalog, len(mappings))
	for _, mapping := range mappings {
		key := PaddlePriceKey{
			Plan:     mapping.Plan,
			Interval: mapping.Interval,
			Tier:     mapping.Tier,
		}
		if _, exists := catalog[key]; exists {
			return PaddleConfig{}, fmt.Errorf("%w: duplicate mapping for %v", ErrInvalidPaddleConfig, key)
		}
		catalog[key] = mapping
	}
	config := PaddleConfig{
		Environment:   PaddleEnvironment(environment),
		APIKey:        apiKey,
		WebhookSecret: webhookSecret,
		Catalog:       catalog,
	}
	return config, config.Validate()
}

func (config PaddleConfig) APIBaseURL() string {
	if config.Environment == PaddleSandbox {
		return "https://sandbox-api.paddle.com"
	}
	return "https://api.paddle.com"
}

func (catalog PaddleCatalog) Validate() error {
	const expectedMappings = 2*2*3 + 2
	if len(catalog) != expectedMappings {
		return fmt.Errorf("%w: exactly %d paid price mappings are required", ErrCatalogMismatch, expectedMappings)
	}
	seenPrices := make(map[string]struct{}, expectedMappings)
	products := make(map[PlanCode]string, 3)
	for _, plan := range []PlanCode{PlanPro, PlanTeam, PlanUnlimited} {
		publicPlan, _ := PublicPlanByCode(plan)
		for _, interval := range []BillingInterval{IntervalMonthly, IntervalAnnual} {
			tiers := []PriceTierCode{TierOne, TierTwo, TierThree}
			if plan == PlanUnlimited {
				tiers = []PriceTierCode{TierFlat}
			}
			for tierIndex, tierCode := range tiers {
				key := PaddlePriceKey{Plan: plan, Interval: interval, Tier: tierCode}
				mapping, ok := catalog[key]
				if !ok {
					return fmt.Errorf("%w: %s/%s/%s", ErrMissingPaddlePrice, plan, interval, tierCode)
				}
				if mapping.Plan != plan ||
					mapping.Interval != interval ||
					mapping.Tier != tierCode {
					return fmt.Errorf("%w: mapping key and value differ", ErrCatalogMismatch)
				}
				if !paddleIDPattern.MatchString(mapping.ProductID) ||
					!strings.HasPrefix(mapping.ProductID, "pro_") ||
					!paddleIDPattern.MatchString(mapping.PriceID) ||
					!strings.HasPrefix(mapping.PriceID, "pri_") {
					return fmt.Errorf("%w: invalid product or price ID", ErrInvalidPaddleConfig)
				}
				if configured, exists := products[plan]; exists && configured != mapping.ProductID {
					return fmt.Errorf("%w: plan %s maps to multiple products", ErrCatalogMismatch, plan)
				}
				products[plan] = mapping.ProductID
				if _, duplicate := seenPrices[mapping.PriceID]; duplicate {
					return fmt.Errorf("%w: duplicate price ID", ErrCatalogMismatch)
				}
				seenPrices[mapping.PriceID] = struct{}{}
				var want int64
				if plan == PlanUnlimited {
					want = publicPlan.Prices.Monthly.AmountCents
					if interval == IntervalAnnual {
						want = publicPlan.Prices.Annual.AmountCents
					}
				} else {
					want = publicPlan.PriceTiers[tierIndex].Monthly.AmountCents
					if interval == IntervalAnnual {
						want = publicPlan.PriceTiers[tierIndex].Annual.AmountCents
					}
				}
				if mapping.UnitAmountCents != want {
					return fmt.Errorf(
						"%w: %s/%s/%s amount is %d, want %d",
						ErrCatalogMismatch,
						plan,
						interval,
						tierCode,
						mapping.UnitAmountCents,
						want,
					)
				}
			}
		}
	}
	if len(products) != 3 ||
		products[PlanPro] == products[PlanTeam] ||
		products[PlanPro] == products[PlanUnlimited] ||
		products[PlanTeam] == products[PlanUnlimited] {
		return fmt.Errorf("%w: paid plans must use distinct products", ErrCatalogMismatch)
	}
	return nil
}

func (catalog PaddleCatalog) ExpectedItems(
	plan PlanCode,
	interval BillingInterval,
	channels *int64,
) ([]PaddleItem, error) {
	publicPlan, err := PublicPlanByCode(plan)
	if err != nil {
		return nil, err
	}
	if !publicPlan.Purchasable {
		return nil, ErrFreePlan
	}
	if !validInterval(interval) {
		return nil, ErrInvalidInterval
	}
	if err := validateChannelQuantity(publicPlan, channels); err != nil {
		return nil, err
	}
	if plan == PlanUnlimited {
		mapping, ok := catalog[PaddlePriceKey{
			Plan:     plan,
			Interval: interval,
			Tier:     TierFlat,
		}]
		if !ok {
			return nil, fmt.Errorf("%w for %s/%s/%s", ErrMissingPaddlePrice, plan, interval, TierFlat)
		}
		return []PaddleItem{{PriceID: mapping.PriceID, Quantity: 1}}, nil
	}
	tiers := []struct {
		code PriceTierCode
		from int64
		to   int64
	}{
		{TierOne, 1, 10},
		{TierTwo, 11, 25},
		{TierThree, 26, 50},
	}
	items := make([]PaddleItem, 0, 3)
	for _, tier := range tiers {
		quantity := tierQuantity(*channels, tier.from, tier.to)
		if quantity == 0 {
			continue
		}
		mapping, ok := catalog[PaddlePriceKey{
			Plan:     plan,
			Interval: interval,
			Tier:     tier.code,
		}]
		if !ok {
			return nil, fmt.Errorf("%w for %s/%s/%s", ErrMissingPaddlePrice, plan, interval, tier.code)
		}
		items = append(items, PaddleItem{PriceID: mapping.PriceID, Quantity: quantity})
	}
	return items, nil
}

func (catalog PaddleCatalog) ResolveItems(
	items []PaddleItem,
) (PlanCode, BillingInterval, *int64, bool) {
	for _, plan := range []PlanCode{PlanPro, PlanTeam, PlanUnlimited} {
		for _, interval := range []BillingInterval{IntervalMonthly, IntervalAnnual} {
			if plan == PlanUnlimited {
				expected, err := catalog.ExpectedItems(plan, interval, nil)
				if err == nil && SamePaddleItems(expected, items) {
					return plan, interval, nil, true
				}
				continue
			}
			publicPlan, _ := PublicPlanByCode(plan)
			for channels := int64(1); channels <= *publicPlan.Limits.Channels; channels++ {
				quantity := channels
				expected, err := catalog.ExpectedItems(plan, interval, &quantity)
				if err == nil && SamePaddleItems(expected, items) {
					return plan, interval, &quantity, true
				}
			}
		}
	}
	return "", "", nil, false
}

func SamePaddleItems(left, right []PaddleItem) bool {
	normalize := func(items []PaddleItem) []PaddleItem {
		copyItems := slices.Clone(items)
		slices.SortFunc(copyItems, func(a, b PaddleItem) int {
			return strings.Compare(a.PriceID, b.PriceID)
		})
		return copyItems
	}
	left = normalize(left)
	right = normalize(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Quantity != right[index].Quantity ||
			!hmac.Equal([]byte(left[index].PriceID), []byte(right[index].PriceID)) {
			return false
		}
	}
	return true
}
