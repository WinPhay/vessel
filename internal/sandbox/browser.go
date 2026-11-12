package sandbox

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/WinPhay/vessel/internal/tokenfilter"
)

var (
	ErrInvalidURL          = errors.New("invalid or unsupported URL protocol (must be http, https, or file)")
	ErrBrowserNotInstalled = errors.New("neither chromium nor curl/wget found in sandbox container")
	ErrScreenshotFailed    = errors.New("failed to capture screenshot in container")
)

// BrowseResult contains sanitized text and metadata from a visited web page.
type BrowseResult struct {
	URL           string `json:"url"`
	Title         string `json:"title"`
	Content       string `json:"content"`
	Engine        string `json:"engine"`
	TokenEstimate int    `json:"token_estimate"`
	Truncated     bool   `json:"truncated"`
}

// ScreenshotResult contains the location and dimensions of a captured screenshot.
type ScreenshotResult struct {
	URL          string `json:"url"`
	RelativePath string `json:"relative_path"`
	FullPath     string `json:"full_path"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	SizeBytes    int64  `json:"size_bytes"`
}

// PageLink represents a hyperlink extracted from a page.
type PageLink struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// PageHeading represents a structural heading tag.
type PageHeading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
}

// PageInteractiveElement represents an actionable UI affordance (button, input, form).
type PageInteractiveElement struct {
	Tag         string `json:"tag"`
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	ID          string `json:"id,omitempty"`
	Text        string `json:"text,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

// ExtractPageResult contains structured elements extracted from a web page.
type ExtractPageResult struct {
	URL           string                   `json:"url"`
	Title         string                   `json:"title"`
	Headings      []PageHeading            `json:"headings"`
	Links         []PageLink               `json:"links"`
	Elements      []PageInteractiveElement `json:"elements"`
	TokenEstimate int                      `json:"token_estimate"`
}

var (
	reTitle       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reScript      = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle       = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reSvg         = regexp.MustCompile(`(?is)<svg[^>]*>.*?</svg>`)
	reNoscript    = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`)
	reComment     = regexp.MustCompile(`(?s)<!--.*?-->`)
	reHeadings    = regexp.MustCompile(`(?is)<h([1-6])[^>]*>(.*?)</h[1-6]>`)
	reAnchor      = regexp.MustCompile(`(?is)<a\s+[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	reButton      = regexp.MustCompile(`(?is)<button[^>]*>(.*?)</button>`)
	reInput       = regexp.MustCompile(`(?is)<input([^>]*)/?>`)
	reAttrType    = regexp.MustCompile(`(?i)type=["']([^"']+)["']`)
	reAttrName    = regexp.MustCompile(`(?i)name=["']([^"']+)["']`)
	reAttrID      = regexp.MustCompile(`(?i)id=["']([^"']+)["']`)
	reAttrPlace   = regexp.MustCompile(`(?i)placeholder=["']([^"']+)["']`)
	reBlockTags   = regexp.MustCompile(`(?i)<(?:p|div|br|li|tr|section|article|header|footer)[^>]*>`)
	reAllTags     = regexp.MustCompile(`<[^>]+>`)
	reExcessSpace = regexp.MustCompile(`[ \t]+`)
	reExcessLines = regexp.MustCompile(`\n{3,}`)
)

// ValidateURL ensures the target URL uses a permitted scheme.
func ValidateURL(rawURL string) error {
	u, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "file" {
		return fmt.Errorf("%w: scheme %q is not permitted", ErrInvalidURL, scheme)
	}
	return nil
}

// ShellEscape quotes a string safely for inclusion in sh/bash commands.
func ShellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// ExtractTitle pulls and unescapes the document title.
func ExtractTitle(rawHTML string) string {
	matches := reTitle.FindStringSubmatch(rawHTML)
	if len(matches) > 1 {
		title := reAllTags.ReplaceAllString(matches[1], "")
		return strings.TrimSpace(html.UnescapeString(title))
	}
	return ""
}

// HTMLToMarkdown converts raw HTML into clean, token-efficient Markdown/text.
func HTMLToMarkdown(rawHTML string, includeLinks bool) string {
	// 1. Strip non-content blocks
	s := reScript.ReplaceAllString(rawHTML, "")
	s = reStyle.ReplaceAllString(s, "")
	s = reSvg.ReplaceAllString(s, "")
	s = reNoscript.ReplaceAllString(s, "")
	s = reComment.ReplaceAllString(s, "")

	// 2. Headings: <hX>...</hX> -> # ...
	s = reHeadings.ReplaceAllStringFunc(s, func(match string) string {
		parts := reHeadings.FindStringSubmatch(match)
		if len(parts) > 2 {
			level, _ := strconv.Atoi(parts[1])
			if level < 1 {
				level = 1
			}
			prefix := strings.Repeat("#", level) + " "
			text := strings.TrimSpace(reAllTags.ReplaceAllString(parts[2], ""))
			return "\n\n" + prefix + text + "\n\n"
		}
		return match
	})

	// 3. Links: <a href="...">text</a> -> [text](href)
	if includeLinks {
		s = reAnchor.ReplaceAllStringFunc(s, func(match string) string {
			parts := reAnchor.FindStringSubmatch(match)
			if len(parts) > 2 {
				href := parts[1]
				text := strings.TrimSpace(reAllTags.ReplaceAllString(parts[2], ""))
				if text == "" {
					return ""
				}
				return fmt.Sprintf("[%s](%s)", text, href)
			}
			return match
		})
	}

	// 4. Block tags to newlines
	s = reBlockTags.ReplaceAllString(s, "\n")

	// 5. Strip all remaining HTML tags
	s = reAllTags.ReplaceAllString(s, "")

	// 6. Unescape HTML entities
	s = html.UnescapeString(s)

	// 7. Normalize whitespace
	lines := strings.Split(s, "\n")
	var cleanedLines []string
	for _, line := range lines {
		line = reExcessSpace.ReplaceAllString(strings.TrimSpace(line), " ")
		cleanedLines = append(cleanedLines, line)
	}
	result := strings.Join(cleanedLines, "\n")
	result = reExcessLines.ReplaceAllString(result, "\n\n")

	return strings.TrimSpace(result)
}

// ExtractPageElements pulls links, headings, and interactive elements.
func ExtractPageElements(rawHTML string) ([]PageHeading, []PageLink, []PageInteractiveElement) {
	// Strip scripts & styles first
	s := reScript.ReplaceAllString(rawHTML, "")
	s = reStyle.ReplaceAllString(s, "")

	// Headings
	var headings []PageHeading
	for _, m := range reHeadings.FindAllStringSubmatch(s, -1) {
		if len(m) > 2 {
			lvl, _ := strconv.Atoi(m[1])
			txt := strings.TrimSpace(reAllTags.ReplaceAllString(m[2], ""))
			if txt != "" {
				headings = append(headings, PageHeading{
					Level: lvl,
					Text:  html.UnescapeString(txt),
				})
			}
		}
	}

	// Links
	var links []PageLink
	seenURLs := make(map[string]bool)
	for _, m := range reAnchor.FindAllStringSubmatch(s, -1) {
		if len(m) > 2 {
			href := strings.TrimSpace(m[1])
			txt := strings.TrimSpace(reAllTags.ReplaceAllString(m[2], ""))
			if href != "" && !seenURLs[href] && !strings.HasPrefix(href, "javascript:") {
				seenURLs[href] = true
				links = append(links, PageLink{
					Text: html.UnescapeString(txt),
					URL:  href,
				})
			}
		}
	}

	// Interactive Elements (Buttons & Inputs)
	var elements []PageInteractiveElement
	for _, m := range reButton.FindAllStringSubmatch(s, -1) {
		if len(m) > 1 {
			txt := strings.TrimSpace(reAllTags.ReplaceAllString(m[1], ""))
			elements = append(elements, PageInteractiveElement{
				Tag:  "button",
				Text: html.UnescapeString(txt),
			})
		}
	}

	for _, m := range reInput.FindAllStringSubmatch(s, -1) {
		if len(m) > 1 {
			attrs := m[1]
			elem := PageInteractiveElement{Tag: "input"}
			if tm := reAttrType.FindStringSubmatch(attrs); len(tm) > 1 {
				elem.Type = tm[1]
			} else {
				elem.Type = "text"
			}
			if nm := reAttrName.FindStringSubmatch(attrs); len(nm) > 1 {
				elem.Name = nm[1]
			}
			if idm := reAttrID.FindStringSubmatch(attrs); len(idm) > 1 {
				elem.ID = idm[1]
			}
			if pm := reAttrPlace.FindStringSubmatch(attrs); len(pm) > 1 {
				elem.Placeholder = pm[1]
			}
			elements = append(elements, elem)
		}
	}

	return headings, links, elements
}

// BrowseURL visits a web page inside the sandbox container and returns sanitized Markdown.
func BrowseURL(ctx context.Context, sb Sandbox, targetURL string, waitSec int, includeLinks bool) (*BrowseResult, error) {
	if err := ValidateURL(targetURL); err != nil {
		return nil, err
	}

	if waitSec <= 0 {
		waitSec = 2
	}

	// Command inside container: probe chromium first for dynamic DOM; fall back to curl/wget
	cmd := fmt.Sprintf(`sh -c '
URL=%s
if command -v chromium-browser >/dev/null 2>&1; then
    echo "===ENGINE:chromium==="
    chromium-browser --headless --disable-gpu --no-sandbox --disable-dev-shm-usage --dump-dom "$URL" 2>/dev/null
elif command -v chromium >/dev/null 2>&1; then
    echo "===ENGINE:chromium==="
    chromium --headless --disable-gpu --no-sandbox --disable-dev-shm-usage --dump-dom "$URL" 2>/dev/null
elif command -v curl >/dev/null 2>&1; then
    echo "===ENGINE:curl==="
    curl -sSL -L -A "Mozilla/5.0 (Vessel-Agent/0.1)" "$URL"
elif command -v wget >/dev/null 2>&1; then
    echo "===ENGINE:wget==="
    wget -qO- "$URL"
else
    echo "===ENGINE:none==="
    echo "ERROR: no browser or HTTP client found in sandbox" >&2
    exit 127
fi
'`, ShellEscape(targetURL))

	res, err := sb.Exec(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("browse execution failed: %w", err)
	}

	rawOutput := res.Stdout
	engine := "unknown"
	if strings.Contains(rawOutput, "===ENGINE:chromium===") {
		engine = "chromium-headless"
		rawOutput = strings.Replace(rawOutput, "===ENGINE:chromium===\n", "", 1)
	} else if strings.Contains(rawOutput, "===ENGINE:curl===") {
		engine = "curl-fallback"
		rawOutput = strings.Replace(rawOutput, "===ENGINE:curl===\n", "", 1)
	} else if strings.Contains(rawOutput, "===ENGINE:wget===") {
		engine = "wget-fallback"
		rawOutput = strings.Replace(rawOutput, "===ENGINE:wget===\n", "", 1)
	}

	title := ExtractTitle(rawOutput)
	markdown := HTMLToMarkdown(rawOutput, includeLinks)

	// Token cap (50KB max ~ 12.5k tokens) to prevent context flooding
	truncated := false
	if len(markdown) > 50*1024 {
		markdown, truncated = tokenfilter.TruncateOutput(markdown, 50*1024)
	}

	return &BrowseResult{
		URL:           targetURL,
		Title:         title,
		Content:       markdown,
		Engine:        engine,
		TokenEstimate: tokenfilter.EstimateTokens(markdown),
		Truncated:     truncated,
	}, nil
}

// TakeScreenshot captures a PNG screenshot of a web page into the workspace sandbox.
func TakeScreenshot(ctx context.Context, sb Sandbox, workspaceRoot, targetURL, outputPath string, fullPage bool, width, height int) (*ScreenshotResult, error) {
	if err := ValidateURL(targetURL); err != nil {
		return nil, err
	}

	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 800
	}

	if outputPath == "" {
		outputPath = fmt.Sprintf(".vessel/screenshots/page-%d.png", time.Now().UnixNano())
	}

	// Securely resolve path against workspace jail
	absTarget, err := ResolveSecurePath(workspaceRoot, outputPath)
	if err != nil {
		return nil, err
	}

	// Ensure parent directory exists on host
	if err := os.MkdirAll(filepath.Dir(absTarget), 0755); err != nil {
		return nil, fmt.Errorf("failed to create screenshot directory: %w", err)
	}

	relPath, err := filepath.Rel(workspaceRoot, absTarget)
	if err != nil {
		relPath = outputPath
	}

	containerPath := "/workspace/" + filepath.ToSlash(relPath)

	cmd := fmt.Sprintf(`sh -c '
URL=%s
OUT=%s
if command -v chromium-browser >/dev/null 2>&1; then
    mkdir -p "$(dirname "$OUT")"
    chromium-browser --headless --disable-gpu --no-sandbox --disable-dev-shm-usage \
        --window-size=%d,%d --screenshot="$OUT" "$URL" 2>/dev/null
elif command -v chromium >/dev/null 2>&1; then
    mkdir -p "$(dirname "$OUT")"
    chromium --headless --disable-gpu --no-sandbox --disable-dev-shm-usage \
        --window-size=%d,%d --screenshot="$OUT" "$URL" 2>/dev/null
else
    echo "ERROR: chromium not found in sandbox" >&2
    exit 127
fi
'`, ShellEscape(targetURL), ShellEscape(containerPath), width, height, width, height)

	_, err = sb.Exec(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("screenshot execution failed: %w", err)
	}

	// Check if screenshot was written; in mock sandbox mode, write a dummy mock PNG for testing
	info, statErr := os.Stat(absTarget)
	if statErr != nil {
		if _, isMock := sb.(*MockSandbox); isMock {
			// Write mock 1-byte PNG indicator for deterministic testing
			_ = os.WriteFile(absTarget, []byte("\x89PNG\r\n\x1a\n"), 0644)
			info, _ = os.Stat(absTarget)
		} else {
			return nil, fmt.Errorf("%w: target %s was not created", ErrScreenshotFailed, relPath)
		}
	}

	var size int64
	if info != nil {
		size = info.Size()
	}

	return &ScreenshotResult{
		URL:          targetURL,
		RelativePath: relPath,
		FullPath:     absTarget,
		Width:        width,
		Height:       height,
		SizeBytes:    size,
	}, nil
}

// ExtractPage navigates to targetURL and extracts structured headings, links, and forms.
func ExtractPage(ctx context.Context, sb Sandbox, targetURL string) (*ExtractPageResult, error) {
	if err := ValidateURL(targetURL); err != nil {
		return nil, err
	}

	cmd := fmt.Sprintf(`sh -c '
URL=%s
if command -v chromium-browser >/dev/null 2>&1; then
    chromium-browser --headless --disable-gpu --no-sandbox --disable-dev-shm-usage --dump-dom "$URL" 2>/dev/null
elif command -v chromium >/dev/null 2>&1; then
    chromium --headless --disable-gpu --no-sandbox --disable-dev-shm-usage --dump-dom "$URL" 2>/dev/null
elif command -v curl >/dev/null 2>&1; then
    curl -sSL -L -A "Mozilla/5.0 (Vessel-Agent/0.1)" "$URL"
elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$URL"
else
    exit 127
fi
'`, ShellEscape(targetURL))

	res, err := sb.Exec(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("extract execution failed: %w", err)
	}

	title := ExtractTitle(res.Stdout)
	headings, links, elements := ExtractPageElements(res.Stdout)

	return &ExtractPageResult{
		URL:           targetURL,
		Title:         title,
		Headings:      headings,
		Links:         links,
		Elements:      elements,
		TokenEstimate: tokenfilter.EstimateTokens(res.Stdout),
	}, nil
}
