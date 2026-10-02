package bedrock

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/quailyquaily/uniai/chat"
)

// CountTokens sends the same native body as InvokeModel to Bedrock Runtime.
// Availability depends on the configured model and region.
func (p *Provider) CountTokens(ctx context.Context, req *chat.Request) (*chat.TokenCount, error) {
	if req == nil {
		return nil, fmt.Errorf("bedrock request is nil")
	}
	if len(req.Tools) > 0 {
		return nil, fmt.Errorf("%w: Bedrock adapter does not support tools", chat.ErrTokenCountUnsupported)
	}
	body, err := buildRequest(req, p.modelArn, true)
	if err != nil {
		return nil, err
	}
	out, err := p.client.CountTokens(ctx, &bedrockruntime.CountTokensInput{ModelId: aws.String(p.modelArn), Input: &types.CountTokensInputMemberInvokeModel{Value: types.InvokeModelTokensRequest{Body: body}}}, p.requestOptions()...)
	if err != nil {
		var validation *types.ValidationException
		if errors.As(err, &validation) {
			message := strings.ToLower(aws.ToString(validation.Message))
			if strings.Contains(message, "count") && (strings.Contains(message, "not support") || strings.Contains(message, "unsupported")) {
				return nil, fmt.Errorf("%w: %w", chat.ErrTokenCountUnsupported, err)
			}
		}
		return nil, err
	}
	if out == nil || out.InputTokens == nil || *out.InputTokens < 0 {
		return nil, fmt.Errorf("bedrock CountTokens: missing or invalid inputTokens")
	}
	return &chat.TokenCount{Model: p.modelArn, InputTokens: int(*out.InputTokens)}, nil
}
