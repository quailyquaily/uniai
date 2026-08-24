package subscription

import "context"

// Credential is the short-lived credential needed by a subscription-backed
// inference request. AccountID is required by the Codex backend and ignored by
// xAI.
type Credential struct {
	AccessToken string
	AccountID   string
}

// CredentialSource is implemented by the caller. The caller owns token
// persistence, refresh synchronization, and refresh-token rotation.
type CredentialSource interface {
	Credential(ctx context.Context) (Credential, error)
	RefreshRejected(ctx context.Context, rejectedAccessToken string) (Credential, error)
}
