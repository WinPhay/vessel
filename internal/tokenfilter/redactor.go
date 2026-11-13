package tokenfilter

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var (
	// Known credential patterns
	anthropicPattern            = regexp.MustCompile(`\bsk-ant-[a-zA-Z0-9_\-]{20,}\b`)
	openAIPattern               = regexp.MustCompile(`\bsk-(?:proj-)?[a-zA-Z0-9_\-]{20,}\b`)
	awsKeyPattern               = regexp.MustCompile(`\b(?:AKIA|ABIA|ACCA|ASIA)[0-9A-Z]{16}\b`)
	awsSecretPattern            = regexp.MustCompile(`(?i)(?:aws_secret_access_key|aws_secret_key|secret_key)\s*[:=]\s*["']?([a-zA-Z0-9/+=]{40})["']?`)
	githubPatPattern            = regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[a-zA-Z0-9]{36,255}\b`)
	githubFineGrainedPattern    = regexp.MustCompile(`\bgithub_pat_[a-zA-Z0-9_]{82}\b`)
	googleAPIKeyPattern         = regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35}\b`)
	slackTokenPattern           = regexp.MustCompile(`\bxox[baprs]-[0-9a-zA-Z\-]{10,80}\b`)
	huggingFacePattern          = regexp.MustCompile(`\bhf_[a-zA-Z0-9]{34,}\b`)
	jwtPattern                  = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)
	bearerPattern               = regexp.MustCompile(`(?i)\bBearer\s+([a-zA-Z0-9_\-\.]{24,})\b`)
	privateKeyPattern           = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9_-]+ )?PRIVATE KEY-----[\s\S]*?-----END (?:[A-Z0-9_-]+ )?PRIVATE KEY-----`)
	databaseURLPattern          = regexp.MustCompile(`(?i)\b(postgres|postgresql|mysql|mongodb|redis|amqp)://([^:\s/]+):([^/\r\n]+)@([a-zA-Z0-9_\-\.]+(?::[0-9]+)?(?:/[^\s"']*)?)`)
	highEntropyCandidatePattern = regexp.MustCompile(`\b[a-zA-Z0-9_\-+/=]{32,128}\b`)
	gitHashPattern              = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Vault manages registered secrets and runs redaction passes over text.
type Vault struct {
	mu      sync.RWMutex
	secrets map[string]string // plaintext value -> secret name
	enabled bool
}

// Global default vault used by CleanOutput
var (
	defaultVaultOnce     sync.Once
	defaultVaultInstance *Vault
)

// DefaultVault returns the singleton default vault.
func DefaultVault() *Vault {
	defaultVaultOnce.Do(func() {
		defaultVaultInstance = NewVault()
	})
	return defaultVaultInstance
}

// NewVault initializes a new in-memory secret vault.
func NewVault() *Vault {
	return &Vault{
		secrets: make(map[string]string),
		enabled: true,
	}
}

// SetEnabled toggles secret redaction.
func (v *Vault) SetEnabled(enabled bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.enabled = enabled
}

// IsEnabled returns whether secret redaction is active.
func (v *Vault) IsEnabled() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.enabled
}

// Register adds a secret key-value pair to the vault.
func (v *Vault) Register(key, value string) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.secrets[value] = key
}

// Clear removes all registered secrets from the vault.
func (v *Vault) Clear() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.secrets = make(map[string]string)
}

// Count returns the number of registered secrets.
func (v *Vault) Count() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.secrets)
}

// ShannonEntropy calculates the Shannon entropy in bits for string s.
func ShannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0.0
	}
	freq := make(map[rune]float64)
	for _, r := range s {
		freq[r]++
	}
	length := float64(len(s))
	entropy := 0.0
	for _, count := range freq {
		p := count / length
		if p > 0 {
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}

// Redact passes text through registered secrets, known patterns, and Shannon entropy scanning.
func (v *Vault) Redact(text string) string {
	if text == "" {
		return ""
	}

	v.mu.RLock()
	enabled := v.enabled
	v.mu.RUnlock()
	if !enabled {
		return text
	}

	result := text

	// 1. Registered Secrets Exact Match (sorted longest value first to prevent substring collision)
	v.mu.RLock()
	type secretEntry struct {
		val  string
		name string
	}
	var entries []secretEntry
	for val, name := range v.secrets {
		entries = append(entries, secretEntry{val: val, name: name})
	}
	v.mu.RUnlock()

	if len(entries) > 0 {
		sort.Slice(entries, func(i, j int) bool {
			return len(entries[i].val) > len(entries[j].val)
		})
		for _, e := range entries {
			result = strings.ReplaceAll(result, e.val, "[REDACTED:"+e.name+"]")
		}
	}

	// 2. Private Cryptographic Keys
	result = privateKeyPattern.ReplaceAllString(result, "[REDACTED:PRIVATE_KEY]")

	// 3. Database URLs with credentials
	result = databaseURLPattern.ReplaceAllString(result, "$1://$2:[REDACTED_PASSWORD]@$4")

	// 4. AWS Secret Access Key key-value pairs
	result = awsSecretPattern.ReplaceAllStringFunc(result, func(m string) string {
		sub := awsSecretPattern.FindStringSubmatch(m)
		if len(sub) > 1 {
			return strings.Replace(m, sub[1], "[REDACTED:AWS_SECRET_KEY]", 1)
		}
		return m
	})

	// 5. Known API Key Signatures (Anthropic before OpenAI so sk-ant- is prioritized)
	result = anthropicPattern.ReplaceAllString(result, "[REDACTED:ANTHROPIC_KEY]")
	result = openAIPattern.ReplaceAllString(result, "[REDACTED:OPENAI_KEY]")
	result = awsKeyPattern.ReplaceAllString(result, "[REDACTED:AWS_ACCESS_KEY]")
	result = githubPatPattern.ReplaceAllString(result, "[REDACTED:GITHUB_PAT]")
	result = githubFineGrainedPattern.ReplaceAllString(result, "[REDACTED:GITHUB_PAT]")
	result = googleAPIKeyPattern.ReplaceAllString(result, "[REDACTED:GOOGLE_API_KEY]")
	result = slackTokenPattern.ReplaceAllString(result, "[REDACTED:SLACK_TOKEN]")
	result = huggingFacePattern.ReplaceAllString(result, "[REDACTED:HUGGINGFACE_KEY]")
	result = jwtPattern.ReplaceAllString(result, "[REDACTED:JWT_TOKEN]")

	// 6. Bearer Tokens
	result = bearerPattern.ReplaceAllStringFunc(result, func(m string) string {
		sub := bearerPattern.FindStringSubmatch(m)
		if len(sub) > 1 {
			return strings.Replace(m, sub[1], "[REDACTED:BEARER_TOKEN]", 1)
		}
		return m
	})

	// 7. Shannon Entropy Scanner for Unprefixed High-Entropy Secrets
	result = highEntropyCandidatePattern.ReplaceAllStringFunc(result, func(candidate string) string {
		// Ignore if already redacted
		if strings.HasPrefix(candidate, "[REDACTED") {
			return candidate
		}
		// Ignore 40-char lowercase git commit hashes
		if len(candidate) == 40 && gitHashPattern.MatchString(candidate) {
			return candidate
		}
		// Calculate entropy
		entropy := ShannonEntropy(candidate)
		// Threshold: >= 4.5 bits/char indicates high randomness typical of base64/alphanumeric secrets
		if len(candidate) >= 32 && entropy >= 4.5 {
			return "[REDACTED:HIGH_ENTROPY_SECRET]"
		}
		return candidate
	})

	return result
}

// RedactOutput redacts sensitive material using the global DefaultVault.
func RedactOutput(text string) string {
	return DefaultVault().Redact(text)
}

// RegisterSecret adds a secret to the global DefaultVault.
func RegisterSecret(key, value string) {
	DefaultVault().Register(key, value)
}

// ClearSecrets clears the global DefaultVault.
func ClearSecrets() {
	DefaultVault().Clear()
}

// SetSecretMasking toggles global secret masking.
func SetSecretMasking(enabled bool) {
	DefaultVault().SetEnabled(enabled)
}
