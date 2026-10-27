package bedrock

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestClaude55ReasoningOptions(t *testing.T) {
	for _, model := range []string{"anthropic.claude-sonnet-5-5", "global.anthropic.claude-sonnet-5-5", "global.anthropic.claude-haiku-5-5"} {
		offMode := "between_tools"
		if strings.Contains(model, "haiku") {
			offMode = "disabled"
		}
		for _, mode := range []string{"", "adaptive", offMode} {
			for _, effort := range []chat.ReasoningEffort{"low", "medium", "high", "xhigh", "max", "none", "minimal", "invalid"} {
				payload := map[string]any{}
				err := applyBedrockReasoningOptions(payload, model, chat.Options{
					ReasoningEffort: &effort, ReasoningDetails: true,
					Bedrock: structs.JSONMap{"thinking_type": mode},
				})
				invalid := effort == "none" || effort == "minimal" || effort == "invalid" ||
					(mode == offMode && (effort == "xhigh" || effort == "max"))
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
				if mode == offMode {
					want = `{"type":"` + offMode + `"}`
				}
				if string(data) != want {
					t.Errorf("%s/%s/%s: got %s, want %s", model, mode, effort, data, want)
				}
			}
		}
	}
}
