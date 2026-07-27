package entitlements

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

type CatalogProvisionAction struct {
	Resource string
	Key      string
	Action   string
}

type CatalogProvisionResult struct {
	Actions []CatalogProvisionAction
	Catalog PaddleCatalog
}

type paddleCatalogProduct struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	TaxCategory string         `json:"tax_category"`
	Status      string         `json:"status"`
	CustomData  map[string]any `json:"custom_data"`
}

type paddleCatalogPrice struct {
	ID           string         `json:"id"`
	ProductID    string         `json:"product_id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Status       string         `json:"status"`
	TaxMode      string         `json:"tax_mode"`
	CustomData   map[string]any `json:"custom_data"`
	BillingCycle struct {
		Interval  string `json:"interval"`
		Frequency int64  `json:"frequency"`
	} `json:"billing_cycle"`
	TrialPeriod any `json:"trial_period"`
	UnitPrice   struct {
		Amount       string `json:"amount"`
		CurrencyCode string `json:"currency_code"`
	} `json:"unit_price"`
	Quantity struct {
		Minimum int64 `json:"minimum"`
		Maximum int64 `json:"maximum"`
	} `json:"quantity"`
}

type paddleListMeta struct {
	Pagination struct {
		Next    string `json:"next"`
		HasMore bool   `json:"has_more"`
	} `json:"pagination"`
}

func NewPaddleCatalogClient(
	environment PaddleEnvironment,
	apiKey string,
	httpClient *http.Client,
) (*PaddleClient, error) {
	if err := validatePaddleEnvironmentAndKey(environment, apiKey); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	config := PaddleConfig{Environment: environment}
	return &PaddleClient{
		apiKey: apiKey, apiBase: config.APIBaseURL(), client: httpClient,
	}, nil
}

func (client *PaddleClient) ProvisionCatalog(
	ctx context.Context,
	manifest PaddleCatalogManifest,
	apply bool,
) (CatalogProvisionResult, error) {
	if err := manifest.Validate(); err != nil {
		return CatalogProvisionResult{}, err
	}
	products, err := client.listCatalogProducts(ctx)
	if err != nil {
		return CatalogProvisionResult{}, err
	}
	result := CatalogProvisionResult{Catalog: make(PaddleCatalog, 14)}
	for _, productSpec := range manifest.Products {
		product, found, err := findCatalogProduct(products, manifest.Version, productSpec.Plan)
		if err != nil {
			return CatalogProvisionResult{}, err
		}
		productAction := CatalogProvisionAction{
			Resource: "product", Key: string(productSpec.Plan), Action: "reuse",
		}
		if !found {
			productAction.Action = "create"
			result.Actions = append(result.Actions, productAction)
			if !apply {
				for _, price := range productSpec.Prices {
					result.Actions = append(result.Actions, CatalogProvisionAction{
						Resource: "price",
						Key:      priceActionKey(productSpec.Plan, price.Interval, price.Tier),
						Action:   "create",
					})
				}
				continue
			}
			product, err = client.createCatalogProduct(ctx, manifest.Version, productSpec)
			if err != nil {
				return CatalogProvisionResult{}, err
			}
		} else {
			if err := validateRemoteProduct(product, productSpec); err != nil {
				return CatalogProvisionResult{}, err
			}
			result.Actions = append(result.Actions, productAction)
		}
		prices, err := client.listCatalogPrices(ctx, product.ID)
		if err != nil {
			return CatalogProvisionResult{}, err
		}
		for _, priceSpec := range productSpec.Prices {
			key := PaddlePriceKey{
				Plan: productSpec.Plan, Interval: priceSpec.Interval, Tier: priceSpec.Tier,
			}
			price, priceFound, err := findCatalogPrice(prices, manifest.Version, key)
			if err != nil {
				return CatalogProvisionResult{}, err
			}
			action := CatalogProvisionAction{
				Resource: "price",
				Key:      priceActionKey(key.Plan, key.Interval, key.Tier),
				Action:   "reuse",
			}
			if !priceFound {
				action.Action = "create"
				result.Actions = append(result.Actions, action)
				if !apply {
					continue
				}
				price, err = client.createCatalogPrice(
					ctx,
					manifest,
					product.ID,
					productSpec.Plan,
					priceSpec,
				)
				if err != nil {
					return CatalogProvisionResult{}, err
				}
			} else {
				if err := validateRemotePrice(
					price,
					product.ID,
					manifest,
					priceSpec,
					key,
				); err != nil {
					return CatalogProvisionResult{}, err
				}
				result.Actions = append(result.Actions, action)
			}
			result.Catalog[key] = PaddlePriceMapping{
				Plan:            key.Plan,
				Interval:        key.Interval,
				Tier:            key.Tier,
				ProductID:       product.ID,
				PriceID:         price.ID,
				UnitAmountCents: priceSpec.UnitAmountCents,
			}
		}
	}
	if apply || len(result.Catalog) == 14 {
		if err := result.Catalog.Validate(); err != nil {
			return CatalogProvisionResult{}, err
		}
	}
	return result, nil
}

func (client *PaddleClient) listCatalogProducts(
	ctx context.Context,
) ([]paddleCatalogProduct, error) {
	path := "/products?per_page=200&status=active,archived"
	var products []paddleCatalogProduct
	for path != "" {
		var response struct {
			Data []paddleCatalogProduct `json:"data"`
			Meta paddleListMeta         `json:"meta"`
		}
		if err := client.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		products = append(products, response.Data...)
		var err error
		path, err = client.nextCatalogPage(response.Meta)
		if err != nil {
			return nil, err
		}
	}
	return products, nil
}

func (client *PaddleClient) listCatalogPrices(
	ctx context.Context,
	productID string,
) ([]paddleCatalogPrice, error) {
	path := "/prices?per_page=200&status=active,archived&product_id=" +
		url.QueryEscape(productID)
	var prices []paddleCatalogPrice
	for path != "" {
		var response struct {
			Data []paddleCatalogPrice `json:"data"`
			Meta paddleListMeta       `json:"meta"`
		}
		if err := client.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		prices = append(prices, response.Data...)
		var err error
		path, err = client.nextCatalogPage(response.Meta)
		if err != nil {
			return nil, err
		}
	}
	return prices, nil
}

func (client *PaddleClient) nextCatalogPage(meta paddleListMeta) (string, error) {
	if !meta.Pagination.HasMore {
		return "", nil
	}
	next, err := url.Parse(meta.Pagination.Next)
	if err != nil || next.Scheme == "" || next.Host == "" {
		return "", fmt.Errorf("invalid Paddle catalog pagination")
	}
	base, err := url.Parse(client.apiBase)
	if err != nil || next.Scheme != base.Scheme || next.Host != base.Host {
		return "", fmt.Errorf("invalid Paddle catalog pagination host")
	}
	return next.RequestURI(), nil
}

func (client *PaddleClient) createCatalogProduct(
	ctx context.Context,
	version string,
	spec PaddleProductManifest,
) (paddleCatalogProduct, error) {
	payload := struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		TaxCategory string            `json:"tax_category"`
		CustomData  map[string]string `json:"custom_data"`
	}{
		Name:        spec.Name,
		Description: spec.Description,
		TaxCategory: spec.TaxCategory,
		CustomData: map[string]string{
			"postqron_catalog_version": version,
			"postqron_plan":            string(spec.Plan),
		},
	}
	var response struct {
		Data paddleCatalogProduct `json:"data"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/products", payload, &response); err != nil {
		return paddleCatalogProduct{}, err
	}
	if err := validateRemoteProduct(response.Data, spec); err != nil {
		return paddleCatalogProduct{}, err
	}
	return response.Data, nil
}

func (client *PaddleClient) createCatalogPrice(
	ctx context.Context,
	manifest PaddleCatalogManifest,
	productID string,
	plan PlanCode,
	spec PaddlePriceManifest,
) (paddleCatalogPrice, error) {
	interval := "month"
	if spec.Interval == IntervalAnnual {
		interval = "year"
	}
	payload := struct {
		ProductID   string `json:"product_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		TaxMode     string `json:"tax_mode"`
		UnitPrice   struct {
			Amount       string `json:"amount"`
			CurrencyCode string `json:"currency_code"`
		} `json:"unit_price"`
		BillingCycle struct {
			Interval  string `json:"interval"`
			Frequency int64  `json:"frequency"`
		} `json:"billing_cycle"`
		Quantity struct {
			Minimum int64 `json:"minimum"`
			Maximum int64 `json:"maximum"`
		} `json:"quantity"`
		CustomData map[string]string `json:"custom_data"`
	}{
		ProductID:   productID,
		Name:        spec.Name,
		Description: spec.Description,
		TaxMode:     manifest.TaxMode,
		CustomData: map[string]string{
			"postqron_catalog_version": manifest.Version,
			"postqron_plan":            string(plan),
			"postqron_interval":        string(spec.Interval),
			"postqron_tier":            string(spec.Tier),
		},
	}
	payload.UnitPrice.Amount = strconv.FormatInt(spec.UnitAmountCents, 10)
	payload.UnitPrice.CurrencyCode = manifest.Currency
	payload.BillingCycle.Interval = interval
	payload.BillingCycle.Frequency = 1
	payload.Quantity.Minimum = spec.QuantityMinimum
	payload.Quantity.Maximum = spec.QuantityMaximum
	var response struct {
		Data paddleCatalogPrice `json:"data"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/prices", payload, &response); err != nil {
		return paddleCatalogPrice{}, err
	}
	key := PaddlePriceKey{Plan: plan, Interval: spec.Interval, Tier: spec.Tier}
	if err := validateRemotePrice(response.Data, productID, manifest, spec, key); err != nil {
		return paddleCatalogPrice{}, err
	}
	return response.Data, nil
}

func findCatalogProduct(
	products []paddleCatalogProduct,
	version string,
	plan PlanCode,
) (paddleCatalogProduct, bool, error) {
	var matches []paddleCatalogProduct
	for _, product := range products {
		if customString(product.CustomData, "postqron_catalog_version") == version &&
			customString(product.CustomData, "postqron_plan") == string(plan) {
			matches = append(matches, product)
		}
	}
	if len(matches) > 1 {
		return paddleCatalogProduct{}, false, fmt.Errorf(
			"%w: multiple Paddle products for %s/%s",
			ErrCatalogMismatch,
			version,
			plan,
		)
	}
	if len(matches) == 0 {
		return paddleCatalogProduct{}, false, nil
	}
	return matches[0], true, nil
}

func findCatalogPrice(
	prices []paddleCatalogPrice,
	version string,
	key PaddlePriceKey,
) (paddleCatalogPrice, bool, error) {
	var matches []paddleCatalogPrice
	for _, price := range prices {
		if customString(price.CustomData, "postqron_catalog_version") == version &&
			customString(price.CustomData, "postqron_plan") == string(key.Plan) &&
			customString(price.CustomData, "postqron_interval") == string(key.Interval) &&
			customString(price.CustomData, "postqron_tier") == string(key.Tier) {
			matches = append(matches, price)
		}
	}
	if len(matches) > 1 {
		return paddleCatalogPrice{}, false, fmt.Errorf(
			"%w: multiple Paddle prices for %s/%s/%s",
			ErrCatalogMismatch,
			key.Plan,
			key.Interval,
			key.Tier,
		)
	}
	if len(matches) == 0 {
		return paddleCatalogPrice{}, false, nil
	}
	return matches[0], true, nil
}

func validateRemoteProduct(
	product paddleCatalogProduct,
	spec PaddleProductManifest,
) error {
	if !paddleIDPattern.MatchString(product.ID) ||
		!strings.HasPrefix(product.ID, "pro_") ||
		product.Name != spec.Name ||
		product.Description != spec.Description ||
		product.TaxCategory != spec.TaxCategory ||
		product.Status != "active" {
		return fmt.Errorf("%w: Paddle product differs for %s", ErrCatalogMismatch, spec.Plan)
	}
	return nil
}

func validateRemotePrice(
	price paddleCatalogPrice,
	productID string,
	manifest PaddleCatalogManifest,
	spec PaddlePriceManifest,
	key PaddlePriceKey,
) error {
	interval := "month"
	if spec.Interval == IntervalAnnual {
		interval = "year"
	}
	if !paddleIDPattern.MatchString(price.ID) ||
		!strings.HasPrefix(price.ID, "pri_") ||
		price.ProductID != productID ||
		price.Name != spec.Name ||
		price.Description != spec.Description ||
		price.Status != "active" ||
		price.TaxMode != manifest.TaxMode ||
		price.UnitPrice.Amount != strconv.FormatInt(spec.UnitAmountCents, 10) ||
		price.UnitPrice.CurrencyCode != manifest.Currency ||
		price.BillingCycle.Interval != interval ||
		price.BillingCycle.Frequency != 1 ||
		price.TrialPeriod != nil ||
		price.Quantity.Minimum != spec.QuantityMinimum ||
		price.Quantity.Maximum != spec.QuantityMaximum {
		return fmt.Errorf(
			"%w: Paddle price differs for %s/%s/%s",
			ErrCatalogMismatch,
			key.Plan,
			key.Interval,
			key.Tier,
		)
	}
	return nil
}

func customString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func priceActionKey(plan PlanCode, interval BillingInterval, tier PriceTierCode) string {
	return string(plan) + "/" + string(interval) + "/" + string(tier)
}

func WriteCatalogProvisionReport(
	writer io.Writer,
	result CatalogProvisionResult,
) error {
	actions := slices.Clone(result.Actions)
	slices.SortFunc(actions, func(left, right CatalogProvisionAction) int {
		return strings.Compare(left.Resource+"/"+left.Key, right.Resource+"/"+right.Key)
	})
	for _, action := range actions {
		if _, err := fmt.Fprintf(
			writer,
			"%s %s %s\n",
			action.Resource,
			action.Key,
			action.Action,
		); err != nil {
			return err
		}
	}
	return nil
}

func WritePaddleCatalogMapping(writer io.Writer, catalog PaddleCatalog) error {
	if err := catalog.Validate(); err != nil {
		return err
	}
	mappings := make([]PaddlePriceMapping, 0, len(catalog))
	for _, mapping := range catalog {
		mappings = append(mappings, mapping)
	}
	slices.SortFunc(mappings, func(left, right PaddlePriceMapping) int {
		return strings.Compare(
			priceActionKey(left.Plan, left.Interval, left.Tier),
			priceActionKey(right.Plan, right.Interval, right.Tier),
		)
	})
	encoder := json.NewEncoder(writer)
	return encoder.Encode(mappings)
}
