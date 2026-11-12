package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateURL(t *testing.T) {
	valid := []string{
		"http://example.com",
		"https://example.com/api/v1?query=test#section",
		"http://localhost:3000",
		"file:///workspace/index.html",
	}
	for _, u := range valid {
		if err := ValidateURL(u); err != nil {
			t.Errorf("ValidateURL(%q) unexpected error: %v", u, err)
		}
	}

	invalid := []string{
		"",
		"ftp://example.com",
		"javascript:alert(1)",
		"data:text/html,<h1>test</h1>",
		"not-a-url",
	}
	for _, u := range invalid {
		if err := ValidateURL(u); err == nil {
			t.Errorf("ValidateURL(%q) expected error, got nil", u)
		}
	}
}

func TestExtractTitle(t *testing.T) {
	htmlSample := `<!DOCTYPE html><html><head><title>Test Page &amp; Title</title></head><body>Hello</body></html>`
	title := ExtractTitle(htmlSample)
	if title != "Test Page & Title" {
		t.Errorf("expected 'Test Page & Title', got %q", title)
	}

	noTitle := `<html><body>No title here</body></html>`
	if ExtractTitle(noTitle) != "" {
		t.Errorf("expected empty title, got %q", ExtractTitle(noTitle))
	}
}

func TestHTMLToMarkdown(t *testing.T) {
	htmlSample := `
<!DOCTYPE html>
<html>
<head>
    <title>Doc Title</title>
    <style>body { color: red; }</style>
    <script>alert("bad");</script>
</head>
<body>
    <h1>Main Heading</h1>
    <p>This is a paragraph with <a href="https://example.com">a link</a> and <b>bold text</b>.</p>
    <svg><circle r="10"/></svg>
    <h2>Sub Heading</h2>
    <div>Div block content with &quot;quotes&quot;.</div>
</body>
</html>
`
	md := HTMLToMarkdown(htmlSample, true)

	if strings.Contains(md, "alert(\"bad\")") {
		t.Errorf("expected script to be stripped, got: %s", md)
	}
	if strings.Contains(md, "color: red") {
		t.Errorf("expected style to be stripped, got: %s", md)
	}
	if !strings.Contains(md, "# Main Heading") {
		t.Errorf("expected '# Main Heading' in output, got: %s", md)
	}
	if !strings.Contains(md, "## Sub Heading") {
		t.Errorf("expected '## Sub Heading' in output, got: %s", md)
	}
	if !strings.Contains(md, "[a link](https://example.com)") {
		t.Errorf("expected markdown link, got: %s", md)
	}
	if !strings.Contains(md, "\"quotes\"") {
		t.Errorf("expected unescaped quotes, got: %s", md)
	}
}

func TestExtractPageElements(t *testing.T) {
	htmlSample := `
<html>
<body>
    <h1>Header 1</h1>
    <h2>Header 2</h2>
    <a href="/login">Log In</a>
    <a href="https://github.com">GitHub</a>
    <form action="/submit">
        <input type="text" name="username" placeholder="Username" id="user-id">
        <input type="password" name="password">
        <button type="submit">Submit Form</button>
    </form>
</body>
</html>
`
	headings, links, elements := ExtractPageElements(htmlSample)

	if len(headings) != 2 {
		t.Fatalf("expected 2 headings, got %d", len(headings))
	}
	if headings[0].Text != "Header 1" || headings[0].Level != 1 {
		t.Errorf("heading 0 mismatch: %+v", headings[0])
	}
	if headings[1].Text != "Header 2" || headings[1].Level != 2 {
		t.Errorf("heading 1 mismatch: %+v", headings[1])
	}

	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}
	if links[0].Text != "Log In" || links[0].URL != "/login" {
		t.Errorf("link 0 mismatch: %+v", links[0])
	}

	if len(elements) != 3 { // 1 button + 2 inputs
		t.Fatalf("expected 3 interactive elements, got %d", len(elements))
	}
	if elements[0].Tag != "button" || elements[0].Text != "Submit Form" {
		t.Errorf("button mismatch: %+v", elements[0])
	}
	if elements[1].Tag != "input" || elements[1].Name != "username" || elements[1].Placeholder != "Username" {
		t.Errorf("input 1 mismatch: %+v", elements[1])
	}
}

func TestBrowseURL_MockSandbox(t *testing.T) {
	ctx := context.Background()
	sb := NewMockSandbox()
	sb.mockStdout = "===ENGINE:chromium===\n<html><head><title>Mock Web</title></head><body><h1>Welcome</h1><p>Vessel browser engine.</p></body></html>"

	res, err := BrowseURL(ctx, sb, "https://example.com", 2, true)
	if err != nil {
		t.Fatalf("BrowseURL failed: %v", err)
	}

	if res.Title != "Mock Web" {
		t.Errorf("expected title 'Mock Web', got %q", res.Title)
	}
	if res.Engine != "chromium-headless" {
		t.Errorf("expected engine 'chromium-headless', got %q", res.Engine)
	}
	if !strings.Contains(res.Content, "# Welcome") {
		t.Errorf("content missing heading: %s", res.Content)
	}
	if res.TokenEstimate <= 0 {
		t.Errorf("expected positive token estimate, got %d", res.TokenEstimate)
	}
}

func TestTakeScreenshot_MockSandbox(t *testing.T) {
	ctx := context.Background()
	sb := NewMockSandbox()
	tmpDir, err := os.MkdirTemp("", "vessel-screenshot-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 1. Valid screenshot
	res, err := TakeScreenshot(ctx, sb, tmpDir, "https://example.com", "shots/test.png", false, 1280, 800)
	if err != nil {
		t.Fatalf("TakeScreenshot failed: %v", err)
	}

	if res.RelativePath != filepath.Join("shots", "test.png") {
		t.Errorf("unexpected relative path: %s", res.RelativePath)
	}
	if res.Width != 1280 || res.Height != 800 {
		t.Errorf("unexpected dimensions: %dx%d", res.Width, res.Height)
	}
	if res.SizeBytes <= 0 {
		t.Errorf("expected positive size, got %d", res.SizeBytes)
	}

	// 2. Jail escaping attempt
	_, err = TakeScreenshot(ctx, sb, tmpDir, "https://example.com", "../../escaped.png", false, 1280, 800)
	if err == nil {
		t.Fatalf("expected error for path escaping jail, got nil")
	}
}

func TestExtractPage_MockSandbox(t *testing.T) {
	ctx := context.Background()
	sb := NewMockSandbox()
	sb.mockStdout = `<html><head><title>Extract Test</title></head><body><h1>API Docs</h1><a href="/v1">V1</a><button>Execute</button></body></html>`

	res, err := ExtractPage(ctx, sb, "https://example.com/docs")
	if err != nil {
		t.Fatalf("ExtractPage failed: %v", err)
	}

	if res.Title != "Extract Test" {
		t.Errorf("unexpected title: %q", res.Title)
	}
	if len(res.Headings) != 1 || res.Headings[0].Text != "API Docs" {
		t.Errorf("unexpected headings: %+v", res.Headings)
	}
	if len(res.Links) != 1 || res.Links[0].URL != "/v1" {
		t.Errorf("unexpected links: %+v", res.Links)
	}
	if len(res.Elements) != 1 || res.Elements[0].Text != "Execute" {
		t.Errorf("unexpected elements: %+v", res.Elements)
	}
}
