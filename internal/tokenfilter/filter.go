package tokenfilter

import (
	"regexp"
	"strconv"
	"strings"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\].*?\x07|\x1b[()][AB012]`)

// CleanOutput strips ANSI escape sequences, normalizes line endings, and cleans terminal artifacts.
func CleanOutput(raw string) string {
	cleaned := ansiRegex.ReplaceAllString(raw, "")
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")
	return strings.TrimSpace(cleaned)
}

// EstimateTokens provides a lightweight token estimate (standard 4 chars per token heuristic).
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	tokens := len(text) / 4
	if tokens == 0 {
		return 1
	}
	return tokens
}

// TruncateOutput caps excessive stdout/stderr to maxBytes while preserving the tail for error debugging.
func TruncateOutput(text string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text, false
	}

	half := maxBytes / 2
	head := text[:half]
	tail := text[len(text)-half:]

	return head + "\n\n... [TRUNCATED " + strconv.Itoa(len(text)-maxBytes) + " BYTES BY VESSEL] ...\n\n" + tail, true
}
