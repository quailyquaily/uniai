package uniai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/httputil"
)

func TestClaudeCacheUsageAndCost(t *testing.T) {
	for _, model := range []string{"claude-sonnet-5-5", "claude-opus-4-6"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				original := httputil.DefaultClient
				t.Cleanup(func() { httputil.DefaultClient = original })
				httputil.DefaultClient = &http.Client{Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					usage := `{"input_tokens":50,"output_tokens":25,"cache_read_input_tokens":300000,"cache_creation_input_tokens":200}`
					body := fmt.Sprintf(`{"model":%q,"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":%s}`, model, usage)
					if stream {
						body = fmt.Sprintf("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":%q,\"usage\":%s}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":25}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", model, usage)
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
				c := New(Config{Provider: "anthropic", AnthropicAPIKey: "test", AnthropicModel: model})
				// A caller-provided tiered catalog catches thresholds based on
				// uncached input instead of total input.
				if model == "claude-opus-4-6" {
					c.cfg.Pricing = &PricingCatalog{Chat: []ChatPricingRule{{Model: model, Tiers: []ChatPricingTier{
						{MaxInputTokens: intPtr(200000), InputUSDPerMillion: 3, OutputUSDPerMillion: 15, CachedInputUSDPerMillion: float64Ptr(.3), CacheCreationInputUSDPerMillion: float64Ptr(3.75)},
						{InputUSDPerMillion: 6, OutputUSDPerMillion: 22.5, CachedInputUSDPerMillion: float64Ptr(.6), CacheCreationInputUSDPerMillion: float64Ptr(7.5)},
					}}}}
				}

				opts := []chat.Option{chat.WithMessages(chat.User("hi"))}
				var final *chat.Usage
				if stream {
					opts = append(opts, chat.WithOnStream(func(e chat.StreamEvent) error {
						if e.Done {
							final = e.Usage
						}
						return nil
					}))
				}
				out, err := c.Chat(context.Background(), opts...)
				if err != nil {
					t.Fatal(err)
				}
				want := chat.Usage{InputTokens: 300250, OutputTokens: 25, TotalTokens: 300275, Cache: chat.UsageCache{CachedInputTokens: 300000, CacheCreationInputTokens: 200}}
				want.Cost, _ = c.cfg.Pricing.EstimateChatCost(model, want)
				if want.Cost == nil || want.Cost.Input == 0 {
					t.Fatal("invalid expected pricing")
				}
				if !reflect.DeepEqual(out.Usage, want) {
					t.Fatalf("usage=%+v cost=%+v; want %+v cost=%+v", out.Usage, out.Usage.Cost, want, want.Cost)
				}
				if stream && (final == nil || !reflect.DeepEqual(*final, want)) {
					t.Fatalf("stream usage=%+v; want %+v", final, want)
				}
			})
		}
	}
}
