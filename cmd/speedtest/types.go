package main

import "time"

type fileConfig struct {
	Model          string       `yaml:"model"`
	Attempts       int          `yaml:"attempts"`
	Temperature    *float64     `yaml:"temperature"`
	EchoText       string       `yaml:"echo_text"`
	TimeoutSeconds int          `yaml:"timeout_seconds"`
	Tests          []testConfig `yaml:"tests"`
}

type testConfig struct {
	Name                   string   `yaml:"name"`
	Provider               string   `yaml:"provider"`
	APIBase                string   `yaml:"api_base"`
	APIKeyRef              string   `yaml:"api_key_ref"`
	CloudflareAccountIDRef string   `yaml:"cloudflare_account_id_ref"`
	CloudflareAPITokenRef  string   `yaml:"cloudflare_api_token_ref"`
	Model                  string   `yaml:"model"`
	Temperature            *float64 `yaml:"temperature"`
	EchoText               string   `yaml:"echo_text"`
	TimeoutSeconds         int      `yaml:"timeout_seconds"`
}

type testResult struct {
	Name                   string
	Provider               string
	Model                  string
	APIBase                string
	APIKeyRef              string
	CloudflareAccountIDRef string
	CloudflareAPITokenRef  string

	SetupError string
	Runs       []attemptResult
	Average    time.Duration
}

type attemptResult struct {
	Index     int
	Duration  time.Duration
	OK        bool
	EchoMatch bool
	Err       string
	Response  string
}
