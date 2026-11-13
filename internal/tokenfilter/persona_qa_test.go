package tokenfilter

import (
	"fmt"
	"strings"
	"testing"
)

// TestSecretVault_PersonaQA runs the 4-tier Persona QA chaos and adversarial test matrix.
func TestSecretVault_PersonaQA(t *testing.T) {
	// =========================================================================
	// TIER 1: The Monkey / Fuzz Tester
	// Unpredictable junk, rapid extremes, huge inputs, null bytes, unicode emojis
	// =========================================================================
	t.Run("Tier1_Monkey_Chaos", func(t *testing.T) {
		// 1. Extreme large string with embedded secret in the center
		hugePrefix := strings.Repeat("A", 100*1024) // 100 KB
		secret := "sk-ant-api03-monkeyChaosSecretToken1234567890abcdef"
		hugeInput := hugePrefix + "\n" + secret + "\n" + hugePrefix

		cleaned := CleanOutput(hugeInput)
		if strings.Contains(cleaned, secret) {
			t.Errorf("Tier 1: secret leaked inside 200KB payload")
		}
		if !strings.Contains(cleaned, "[REDACTED:ANTHROPIC_KEY]") {
			t.Errorf("Tier 1: expected redacted marker inside large payload")
		}

		// 2. Fuzz with null bytes and control chars
		nullJunk := "\x00\x00sk-proj-nullByteSecret1234567890abcdef\x00\x01\x02"
		cleanedNull := CleanOutput(nullJunk)
		if strings.Contains(cleanedNull, "nullByteSecret") {
			t.Errorf("Tier 1: secret leaked across null bytes")
		}

		// 3. Burmese Unicode text with embedded OpenAI key
		burmeseInput := "မင်္ဂလာပါ sk-1234567890abcdef1234567890abcdef လျှို့ဝှက်ကုဒ်ဖြစ်သည်"
		cleanedBurmese := CleanOutput(burmeseInput)
		if strings.Contains(cleanedBurmese, "1234567890abcdef") {
			t.Errorf("Tier 1: secret leaked inside Burmese unicode string")
		}
		if !strings.Contains(cleanedBurmese, "[REDACTED:OPENAI_KEY]") {
			t.Errorf("Tier 1: expected OpenAI redaction marker in Burmese text")
		}
	})

	// =========================================================================
	// TIER 2: The Blind Box / Accidental Developer
	// Realistic developer environment dumps, messy .env files, config dumps
	// =========================================================================
	t.Run("Tier2_BlindBox_EnvDumps", func(t *testing.T) {
		envDump := `
# Production Database Connection
DATABASE_URL="postgres://admin:p@ssw0rd123!@db-prod.vpc.internal:5432/core_db?sslmode=require"

# Cloud Credentials
AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPL2
aws_secret_access_key="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"

# AI Provider API Keys
OPENAI_API_KEY="sk-proj-realProdOpenAIKey998877665544332211"
ANTHROPIC_API_KEY=sk-ant-api03-claudeSonnetProdKey1122334455
GITHUB_TOKEN=ghp_myRealGitHubPersonalAccessToken1234567

# Public config that should NOT be redacted
APP_ENV=production
PORT=8080
PUBLIC_URL=https://myapp.com
`
		cleaned := CleanOutput(envDump)

		// Secrets must be redacted
		forbidden := []string{
			"p@ssw0rd123!",
			"AKIAIOSFODNN7EXAMPL2",
			"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"sk-proj-realProdOpenAIKey998877665544332211",
			"sk-ant-api03-claudeSonnetProdKey1122334455",
			"ghp_myRealGitHubPersonalAccessToken1234567",
		}
		for _, f := range forbidden {
			if strings.Contains(cleaned, f) {
				t.Errorf("Tier 2: secret %q leaked in .env dump", f)
			}
		}

		// Safe non-secret settings MUST be preserved
		safeStrings := []string{
			"APP_ENV=production",
			"PORT=8080",
			"PUBLIC_URL=https://myapp.com",
			"postgres://admin:[REDACTED_PASSWORD]@db-prod.vpc.internal:5432/core_db?sslmode=require",
		}
		for _, s := range safeStrings {
			if !strings.Contains(cleaned, s) {
				t.Errorf("Tier 2: safe configuration was improperly corrupted or missing: %q", s)
			}
		}
	})

	// =========================================================================
	// TIER 3: The Semi-Tech Agent
	// Realistic agent behavior: multiline keys, git logs, stack traces
	// =========================================================================
	t.Run("Tier3_SemiTech_AgentScenarios", func(t *testing.T) {
		// 1. Multiline RSA Private Key in stack trace
		stackTrace := `
Goroutine 1 [running]:
main.loadKey()
    /workspace/auth.go:42 +0x120
-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA0YxyzABC1234567890FakeKeyMaterial
fakeLine2ABCDEFGHIJKLMOPQRSTUV
-----END RSA PRIVATE KEY-----
Error: failed to verify signature
`
		cleaned := CleanOutput(stackTrace)
		if strings.Contains(cleaned, "MIIEowIBAAKCAQEA0Yxyz") {
			t.Errorf("Tier 3: private key leaked in stack trace")
		}
		if !strings.Contains(cleaned, "[REDACTED:PRIVATE_KEY]") {
			t.Errorf("Tier 3: expected private key redaction badge")
		}
		if !strings.Contains(cleaned, "Error: failed to verify signature") {
			t.Errorf("Tier 3: error message must be preserved")
		}

		// 2. Bearer authorization header with dynamic token
		reqDump := "GET /api/v1/user HTTP/1.1\nHost: api.internal\nAuthorization: Bearer secretBearerAuthToken1234567890123456\n"
		cleanedReq := CleanOutput(reqDump)
		if strings.Contains(cleanedReq, "secretBearerAuthToken") {
			t.Errorf("Tier 3: bearer token leaked")
		}
		if !strings.Contains(cleanedReq, "Bearer [REDACTED:BEARER_TOKEN]") {
			t.Errorf("Tier 3: expected Bearer redaction format, got:\n%s", cleanedReq)
		}
	})

	// =========================================================================
	// TIER 4: The Adversarial Adversary
	// Deliberate attempts to bypass redaction using ANSI escapes, split tokens,
	// mixed casing, and synthetic vault collisions
	// =========================================================================
	t.Run("Tier4_Adversarial_Attacks", func(t *testing.T) {
		// 1. ANSI Injection Attack: Attacker tries to hide secret by sandwiching ANSI color escapes
		// e.g. "sk-\x1b[31mproj-1234567890abcdefghijklmnopqr\x1b[0m"
		ansiInjected := "sk-\x1b[31mproj-1234567890abcdefghijklmnopqrstuvwxyz\x1b[0m"
		cleanedAnsi := CleanOutput(ansiInjected)
		if strings.Contains(cleanedAnsi, "1234567890abcdef") {
			t.Errorf("Tier 4: ANSI injection bypassed secret masking: got %q", cleanedAnsi)
		}
		if !strings.Contains(cleanedAnsi, "[REDACTED:OPENAI_KEY]") {
			t.Errorf("Tier 4: expected ANSI-cleaned string to be redacted as OPENAI_KEY, got %q", cleanedAnsi)
		}

		// 2. Synthetic Vault Collisions & Ordering:
		// Register a shorter secret that is a prefix of a longer secret
		vault := NewVault()
		vault.Register("SHORT", "secret_val")
		vault.Register("LONG", "secret_val_extended_token_123")

		collisionTest := "Target token is: secret_val_extended_token_123"
		redactedCollision := vault.Redact(collisionTest)
		// Longest match must win: [REDACTED:LONG] not [REDACTED:SHORT]_extended_token_123
		if !strings.Contains(redactedCollision, "[REDACTED:LONG]") {
			t.Errorf("Tier 4: Substring collision failed, expected [REDACTED:LONG], got: %s", redactedCollision)
		}
		if strings.Contains(redactedCollision, "[REDACTED:SHORT]") {
			t.Errorf("Tier 4: Short prefix improperly corrupted long secret match: %s", redactedCollision)
		}

		// 3. High-Entropy Secret Anomaly Detection
		// Unprefixed random 36-char base64 token
		unprefixedSecret := "X9kL2+pQ8=wM4/vR1-zT7_yU0+oE5-sD8_fG"
		entropyTest := fmt.Sprintf("Authentication header: token %s", unprefixedSecret)
		redactedEntropy := CleanOutput(entropyTest)
		if strings.Contains(redactedEntropy, unprefixedSecret) {
			t.Errorf("Tier 4: high-entropy unprefixed secret escaped redaction: %s", redactedEntropy)
		}
	})
}
