package anthropicstream

import (
	"encoding/json"
	"strings"

	"github.com/quailyquaily/uniai/chat"
)

type Event struct {
	Type         string `json:"type"`
	Index        int    `json:"index,omitempty"`
	ContentBlock struct {
		Type      string `json:"type"`
		Thinking  string `json:"thinking,omitempty"`
		Signature string `json:"signature,omitempty"`
		Data      string `json:"data,omitempty"`
	} `json:"content_block,omitempty"`
	Delta struct {
		Type      string `json:"type"`
		Thinking  string `json:"thinking,omitempty"`
		Signature string `json:"signature,omitempty"`
	} `json:"delta,omitempty"`
	Raw json.RawMessage `json:"-"`
}

func Decode(data []byte) (Event, error) {
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		return Event{}, err
	}
	event.Raw = append(json.RawMessage(nil), data...)
	return event, nil
}

type ReasoningState struct {
	blocks          map[int]*reasoningBlock
	order           []int
	thinkingIndexes map[int]int
}

type reasoningBlock struct {
	typeName  string
	text      string
	signature string
	data      string
}

func (s *ReasoningState) Apply(event Event, enabled bool) *chat.StreamEvent {
	if !enabled {
		return nil
	}

	switch event.Type {
	case "content_block_start":
		switch event.ContentBlock.Type {
		case "thinking", "redacted_thinking":
			block := s.block(event.Index, event.ContentBlock.Type)
			block.text = event.ContentBlock.Thinking
			block.signature = event.ContentBlock.Signature
			block.data = event.ContentBlock.Data
		}
	case "content_block_delta":
		switch event.Delta.Type {
		case "thinking_delta":
			block := s.block(event.Index, "thinking")
			block.text += event.Delta.Thinking
			if event.Delta.Thinking == "" {
				return nil
			}
			return &chat.StreamEvent{
				ReasoningDelta: &chat.ReasoningDelta{
					Index: s.thinkingIndex(event.Index),
					Type:  chat.ReasoningDeltaThinking,
					Delta: event.Delta.Thinking,
				},
				Raw: event.Raw,
			}
		case "signature_delta":
			block := s.block(event.Index, "thinking")
			block.signature += event.Delta.Signature
		}
	}
	return nil
}

func (s *ReasoningState) Result() *chat.ReasoningResult {
	if s == nil || len(s.order) == 0 {
		return nil
	}
	result := &chat.ReasoningResult{}
	for _, index := range s.order {
		block := s.blocks[index]
		if block == nil || (strings.TrimSpace(block.text) == "" && strings.TrimSpace(block.signature) == "" && strings.TrimSpace(block.data) == "") {
			continue
		}
		if block.typeName == "thinking" && strings.TrimSpace(block.text) != "" {
			result.Summary = append(result.Summary, block.text)
		}
		result.Blocks = append(result.Blocks, chat.ReasoningBlock{
			Type:      block.typeName,
			Text:      block.text,
			Signature: block.signature,
			Data:      block.data,
		})
	}
	if len(result.Summary) == 0 && len(result.Blocks) == 0 {
		return nil
	}
	return result
}

func (s *ReasoningState) block(index int, typeName string) *reasoningBlock {
	if s.blocks == nil {
		s.blocks = make(map[int]*reasoningBlock)
	}
	block := s.blocks[index]
	if block == nil {
		block = &reasoningBlock{typeName: typeName}
		s.blocks[index] = block
		s.order = append(s.order, index)
	} else if block.typeName == "" {
		block.typeName = typeName
	}
	return block
}

func (s *ReasoningState) thinkingIndex(blockIndex int) int {
	if s.thinkingIndexes == nil {
		s.thinkingIndexes = make(map[int]int)
	}
	index, ok := s.thinkingIndexes[blockIndex]
	if !ok {
		index = len(s.thinkingIndexes)
		s.thinkingIndexes[blockIndex] = index
	}
	return index
}
