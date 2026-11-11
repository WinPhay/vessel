package tokenfilter

import (
	"strings"
	"testing"
)

func TestCleanOutput(t *testing.T) {
	raw := "\x1b[31mError:\x1b[0m build failed\r\n\x1b[1mLine 42\x1b[0m"
	expected := "Error: build failed\nLine 42"

	cleaned := CleanOutput(raw)
	if cleaned != expected {
		t.Errorf("CleanOutput mismatch:\ngot:  %q\nwant: %q", cleaned, expected)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("EstimateTokens(\"\") = %d; want 0", got)
	}

	text := "This is a 32 character test string"
	tokens := EstimateTokens(text)
	if tokens < 7 || tokens > 9 {
		t.Errorf("EstimateTokens unexpected range: %d", tokens)
	}
}

func TestTruncateOutput(t *testing.T) {
	text := strings.Repeat("A", 100)
	truncated, wasTruncated := TruncateOutput(text, 40)

	if !wasTruncated {
		t.Errorf("expected TruncateOutput to report true for wasTruncated")
	}

	if !strings.Contains(truncated, "TRUNCATED 60 BYTES BY VESSEL") {
		t.Errorf("expected truncation banner in output, got: %s", truncated)
	}
}
