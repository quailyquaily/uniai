package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestSonnet55ReasoningOptions(t *testing.T) {
	for _, model := range []string{"anthropic.claude-sonnet-5-5", "global.anthropic.claude-sonnet-5-5"} {
		for _, mode := range []string{"", "adaptive", "between_tools"} {
			for _, effort := range []chat.ReasoningEffort{"low", "medium", "high", "xhigh", "max", "none", "minimal", "invalid"} {
				payload := map[string]any{}
				err := applyBedrockReasoningOptions(payload, model, chat.Options{
					ReasoningEffort: &effort, ReasoningDetails: true,
					Bedrock: structs.JSONMap{"thinking_type": mode},
				})
				invalid := effort == "none" || effort == "minimal" || effort == "invalid" ||
					(mode == "between_tools" && (effort == "xhigh" || effort == "max"))
				if invalid {
					if err == nil {
						t.Errorf("%s/%s/%s: expected error", model, mode, effort)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				data, _ := json.Marshal(payload["thinking"])
				want := `{"display":"summarized","type":"adaptive"}`
				if mode == "between_tools" {
					want = `{"type":"between_tools"}`
				}
				if string(data) != want {
					t.Errorf("%s/%s/%s: got %s, want %s", model, mode, effort, data, want)
				}
			}
		}
	}
}
