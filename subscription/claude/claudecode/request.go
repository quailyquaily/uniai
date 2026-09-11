// Package claudecode contains the HTTP-level Claude Code subscription profile.
// It does not run the CLI or reproduce its TLS fingerprint or billing signatures.
package claudecode

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	MessagesURL    = "https://api.anthropic.com/v1/messages?beta=true"
	DefaultVersion = "2.1.258"
	Identity       = "You are Claude Code, Anthropic's official CLI for Claude."
)

// Profile controls the compatibility identity, not the user's prompt or tools.
// A zero SessionID creates a fresh conversation per Chat call. Set a UUID to
// keep an application-managed conversation stable across turns.
type Profile struct {
	Version   string
	DeviceID  string
	SessionID string
}

// Prepare adds the CLI identity system block and replaces metadata.user_id with
// account-bound metadata. Other fields (including cache controls, tool names,
// tool IDs, and JSON numbers) are preserved. Input bytes are never modified.
func (p Profile) Prepare(data []byte, accountID string) ([]byte, string, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(data, &body); err != nil || body == nil {
		return nil, "", fmt.Errorf("Claude Code body must be a JSON object")
	}
	var system []json.RawMessage
	if raw := body["system"]; len(raw) != 0 && string(raw) != "null" {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			if text != "" {
				part, _ := json.Marshal(map[string]string{"type": "text", "text": text})
				system = append(system, part)
			}
		} else if json.Unmarshal(raw, &system) != nil {
			return nil, "", fmt.Errorf("Claude Code system must be text or content blocks")
		}
	}
	hasIdentity := false
	for _, raw := range system {
		var part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &part) != nil || part.Type != "text" {
			return nil, "", fmt.Errorf("Claude Code system must contain text blocks")
		}
		if part.Text == Identity {
			hasIdentity = true
		}
	}
	if !hasIdentity {
		part, _ := json.Marshal(map[string]string{"type": "text", "text": Identity})
		system = append([]json.RawMessage{part}, system...)
	}
	body["system"], _ = json.Marshal(system)
	session := strings.TrimSpace(p.SessionID)
	if session == "" {
		var err error
		session, err = newUUID()
		if err != nil {
			return nil, "", err
		}
	}
	device := strings.TrimSpace(p.DeviceID)
	if device == "" {
		// Stable across refreshes and process restarts; no host information or
		// access token enters the device identity.
		digest := sha256.Sum256([]byte("uniai/claudecode/" + accountID))
		device = hex.EncodeToString(digest[:])
	}
	identity, _ := json.Marshal(map[string]string{"device_id": device, "account_uuid": accountID, "session_id": session})
	metadata := make(map[string]json.RawMessage)
	if raw := body["metadata"]; len(raw) != 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &metadata) != nil {
			return nil, "", fmt.Errorf("Claude Code metadata must be an object")
		}
	}
	metadata["user_id"], _ = json.Marshal(string(identity))
	body["metadata"], _ = json.Marshal(metadata)
	output, err := json.Marshal(body)
	return output, session, err
}

// ApplyHeaders owns authentication and the CLI profile. Caller beta flags are
// retained and deduplicated, but cannot remove the two subscription betas.
func (p Profile) ApplyHeaders(header http.Header, accessToken, sessionID string) error {
	requestID, err := newUUID()
	if err != nil {
		return err
	}
	betas := []string{"claude-code-20250219", "oauth-2025-04-20"}
	seen := map[string]bool{betas[0]: true, betas[1]: true}
	for name, values := range header {
		if strings.EqualFold(name, "anthropic-beta") {
			for _, value := range values {
				for _, beta := range strings.Split(value, ",") {
					beta = strings.TrimSpace(beta)
					if beta != "" && !seen[beta] {
						betas = append(betas, beta)
						seen[beta] = true
					}
				}
			}
		}
		switch strings.ToLower(name) {
		case "authorization", "proxy-authorization", "x-api-key", "api-key", "cookie", "anthropic-beta", "user-agent", "content-type", "accept", "host", "x-client-request-id":
			delete(header, name)
		default:
			if strings.HasPrefix(strings.ToLower(name), "x-stainless-") || strings.HasPrefix(strings.ToLower(name), "x-claude-code-") || strings.EqualFold(name, "x-app") || strings.EqualFold(name, "anthropic-version") {
				delete(header, name)
			}
		}
	}
	version := strings.TrimSpace(p.Version)
	if version == "" {
		version = DefaultVersion
	}
	for key, value := range map[string]string{
		"Authorization": "Bearer " + strings.TrimSpace(accessToken), "Content-Type": "application/json", "Accept": "application/json",
		"Anthropic-Version": "2023-06-01", "Anthropic-Beta": strings.Join(betas, ","),
		"User-Agent": "claude-cli/" + version + " (external, cli)", "X-App": "cli",
		"X-Stainless-Lang": "js", "X-Stainless-Runtime": "node", "X-Stainless-Retry-Count": "0",
		"X-Claude-Code-Session-Id": sessionID,
		"X-Client-Request-Id":      requestID,
	} {
		header.Set(key, value)
	}
	return nil
}

func newUUID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]), nil
}
