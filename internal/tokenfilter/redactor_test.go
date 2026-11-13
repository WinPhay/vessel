package tokenfilter

import (
	"strings"
	"testing"
)

func TestRedactKnownPatterns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "OpenAI Key Legacy",
			input:    "API_KEY=sk-1234567890abcdef1234567890abcdef and more",
			expected: "API_KEY=[REDACTED:OPENAI_KEY] and more",
		},
		{
			name:     "OpenAI Key Project",
			input:    "Bearer sk-proj-1234567890abcdefghijklmnopqrstuvwxyz123456",
			expected: "Bearer [REDACTED:OPENAI_KEY]",
		},
		{
			name:     "Anthropic Key",
			input:    "export ANTHROPIC_API_KEY=sk-ant-api03-1234567890abcdef1234567890abcdef",
			expected: "export ANTHROPIC_API_KEY=[REDACTED:ANTHROPIC_KEY]",
		},
		{
			name:     "AWS Access Key",
			input:    "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			expected: "AWS_ACCESS_KEY_ID=[REDACTED:AWS_ACCESS_KEY]",
		},
		{
			name:     "AWS Secret Key",
			input:    "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			expected: "aws_secret_access_key = [REDACTED:AWS_SECRET_KEY]",
		},
		{
			name:     "GitHub PAT",
			input:    "Authorization: token ghp_1234567890abcdefghijklmnopqrstuvwxyz12",
			expected: "Authorization: token [REDACTED:GITHUB_PAT]",
		},
		{
			name:     "Google API Key",
			input:    "key=AIzaSyD-1234567890abcdefghijklmnopqrstu",
			expected: "key=[REDACTED:GOOGLE_API_KEY]",
		},
		{
			name:     "Slack Token",
			input:    "xoxb-mock-test-slack-token-1234567890",
			expected: "[REDACTED:SLACK_TOKEN]",
		},
		{
			name:     "HuggingFace Token",
			input:    "hf_1234567890abcdefghijklmnopqrstuvwxyz12",
			expected: "[REDACTED:HUGGINGFACE_KEY]",
		},
		{
			name:     "Database URL Password",
			input:    "DATABASE_URL=postgres://postgres:SuperSecretPassword123@db.internal:5432/app",
			expected: "DATABASE_URL=postgres://postgres:[REDACTED_PASSWORD]@db.internal:5432/app",
		},
		{
			name:     "Private Key",
			input:    "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0Y...\n-----END RSA PRIVATE KEY-----",
			expected: "[REDACTED:PRIVATE_KEY]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactOutput(tt.input)
			if got != tt.expected {
				t.Errorf("RedactOutput mismatch:\ngot:  %q\nwant: %q", got, tt.expected)
			}
		})
	}
}

func TestDynamicVault(t *testing.T) {
	v := NewVault()
	v.Register("STRIPE_KEY", "whsec_customLiveStripeWebhookSecret12345")
	v.Register("JWT_SECRET", "super-secret-signing-key-xyz987")

	input := "Connecting to webhook with whsec_customLiveStripeWebhookSecret12345 and signature super-secret-signing-key-xyz987"
	expected := "Connecting to webhook with [REDACTED:STRIPE_KEY] and signature [REDACTED:JWT_SECRET]"

	got := v.Redact(input)
	if got != expected {
		t.Errorf("Vault Redact mismatch:\ngot:  %q\nwant: %q", got, expected)
	}

	// Test disabling
	v.SetEnabled(false)
	if gotDisabled := v.Redact(input); gotDisabled != input {
		t.Errorf("expected disabled vault to return input unchanged, got: %q", gotDisabled)
	}

	// Test clearing
	v.SetEnabled(true)
	v.Clear()
	if v.Count() != 0 {
		t.Errorf("expected count 0 after Clear, got: %d", v.Count())
	}
}

func TestShannonEntropyScanner(t *testing.T) {
	// A high-entropy random string (e.g. 48-char random base64 secret)
	highEntropySecret := "aB9+xK2/mN8=vP1-qR4_zT7+yW3/uI0=oE5-sD8_fG2="
	input := "Secret received: " + highEntropySecret

	v := NewVault()
	got := v.Redact(input)
	if !strings.Contains(got, "[REDACTED:HIGH_ENTROPY_SECRET]") {
		t.Errorf("expected high-entropy secret to be redacted, got: %s", got)
	}

	// Git commit hash (40 lowercase hex characters) should NOT be redacted
	gitCommit := "e82ed82b4501234567890abcdef1234567890abc"
	inputGit := "commit " + gitCommit + " (HEAD -> main)"
	gotGit := v.Redact(inputGit)
	if !strings.Contains(gotGit, gitCommit) {
		t.Errorf("expected git commit hash to be preserved, got: %s", gotGit)
	}
}

func TestGlobalRegistration(t *testing.T) {
	defer ClearSecrets()

	RegisterSecret("API_TOKEN", "custom-runtime-secret-token-12345")
	output := CleanOutput("Token was: custom-runtime-secret-token-12345\n")
	expected := "Token was: [REDACTED:API_TOKEN]"

	if output != expected {
		t.Errorf("CleanOutput mismatch:\ngot:  %q\nwant: %q", output, expected)
	}
}
