package uniai

import (
	_ "embed"
	"fmt"
	"sync"
)

//go:embed pricing.example.yaml
var embeddedDefaultPricingYAML []byte

var (
	defaultPricingOnce    sync.Once
	defaultPricingCatalog *PricingCatalog
	defaultPricingErr     error
)

// DefaultPricingCatalog returns a cloned copy of the embedded default pricing
// catalog used when Config.Pricing is nil.
func DefaultPricingCatalog() *PricingCatalog {
	defaultPricingOnce.Do(func() {
		defaultPricingCatalog, defaultPricingErr = ParsePricingYAML(embeddedDefaultPricingYAML)
	})
	if defaultPricingErr != nil {
		panic(fmt.Sprintf("uniai: parse embedded default pricing catalog: %v", defaultPricingErr))
	}
	return defaultPricingCatalog.Clone()
}
