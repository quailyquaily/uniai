package uniai

import "github.com/quailyquaily/uniai/chat"

func mergeChatUsage(usages ...chat.Usage) chat.Usage {
	var out chat.Usage
	for _, usage := range usages {
		addChatUsage(&out, usage)
	}
	out.Cost = nil
	return out
}

func addChatUsage(dst *chat.Usage, src chat.Usage) {
	if dst == nil {
		return
	}
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens

	total := src.TotalTokens
	if total == 0 && (src.InputTokens > 0 || src.OutputTokens > 0) {
		total = src.InputTokens + src.OutputTokens
	}
	dst.TotalTokens += total

	dst.Cache.CachedInputTokens += src.Cache.CachedInputTokens
	dst.Cache.CacheCreationInputTokens += src.Cache.CacheCreationInputTokens
	addUsageDetails(&dst.Cache, src.Cache.Details)
	dst.Cost = nil
}

func mergeChatUsageCost(costs ...*chat.UsageCost) *chat.UsageCost {
	var out *chat.UsageCost
	for _, cost := range costs {
		if cost == nil {
			continue
		}
		if out == nil {
			out = cloneChatUsageCost(cost)
			continue
		}
		out.Input = roundUSD(out.Input + cost.Input)
		out.CachedInput = roundUSD(out.CachedInput + cost.CachedInput)
		out.CacheCreationInput = roundUSD(out.CacheCreationInput + cost.CacheCreationInput)
		out.Output = roundUSD(out.Output + cost.Output)
		out.Total = roundUSD(out.Total + cost.Total)
		if out.Currency == "" {
			out.Currency = cost.Currency
		}
		out.Estimated = out.Estimated || cost.Estimated
	}
	return out
}

func cloneChatUsageCost(in *chat.UsageCost) *chat.UsageCost {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func addUsageDetails(dst *chat.UsageCache, details map[string]int) {
	if dst == nil || len(details) == 0 {
		return
	}
	if dst.Details == nil {
		dst.Details = make(map[string]int, len(details))
	}
	for key, value := range details {
		if key == "" || value == 0 {
			continue
		}
		dst.Details[key] += value
		if dst.Details[key] == 0 {
			delete(dst.Details, key)
		}
	}
	if len(dst.Details) == 0 {
		dst.Details = nil
	}
}

func wrapPrefixedChatStreamUsage(prefix chat.Usage, prefixCostComplete bool, onStream chat.OnStreamFunc) chat.OnStreamFunc {
	if onStream == nil {
		return nil
	}
	return func(ev chat.StreamEvent) error {
		if ev.Done && ev.Usage != nil {
			usage := mergeChatUsage(prefix, *ev.Usage)
			if prefixCostComplete && (ev.Usage.Cost != nil || !hasPricableUsage(*ev.Usage)) {
				usage.Cost = mergeChatUsageCost(prefix.Cost, ev.Usage.Cost)
			}
			ev.Usage = &usage
		}
		return onStream(ev)
	}
}
