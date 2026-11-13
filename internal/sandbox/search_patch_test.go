package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepWorkspace(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-search-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Setup files
	f1 := filepath.Join(tempDir, "main.go")
	_ = os.WriteFile(f1, []byte("package main\n\nfunc RunApp() {\n\t// Initialize Engine\n\tprintln(\"Engine Started\")\n}\n"), 0644)

	f2 := filepath.Join(tempDir, "engine.ts")
	_ = os.WriteFile(f2, []byte("export const startEngine = () => {\n  console.log('engine running');\n};\n"), 0644)

	hiddenDir := filepath.Join(tempDir, ".git")
	_ = os.MkdirAll(hiddenDir, 0755)
	_ = os.WriteFile(filepath.Join(hiddenDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0644)

	// 1. Search "Engine" case-insensitive
	res, err := GrepWorkspace(tempDir, GrepOptions{
		Pattern: "engine",
	})
	if err != nil {
		t.Fatalf("GrepWorkspace failed: %v", err)
	}
	if res.TotalMatches < 3 {
		t.Errorf("expected at least 3 matches for 'engine', got %d", res.TotalMatches)
	}
	if res.TokenEstimate <= 0 {
		t.Errorf("expected positive token estimate, got %d", res.TokenEstimate)
	}

	// 2. Search "Engine" case-sensitive
	resCase, err := GrepWorkspace(tempDir, GrepOptions{
		Pattern:       "Engine",
		CaseSensitive: true,
	})
	if err != nil {
		t.Fatalf("GrepWorkspace case-sensitive failed: %v", err)
	}
	for _, m := range resCase.Matches {
		if !strings.Contains(m.LineText, "Engine") {
			t.Errorf("case-sensitive match contained unexpected line: %s", m.LineText)
		}
	}

	// 3. Glob filter *.go
	resGlob, err := GrepWorkspace(tempDir, GrepOptions{
		Pattern:     "engine",
		FilePattern: "*.go",
	})
	if err != nil {
		t.Fatalf("GrepWorkspace with glob failed: %v", err)
	}
	for _, m := range resGlob.Matches {
		if !strings.HasSuffix(m.File, ".go") {
			t.Errorf("expected only .go files, got: %s", m.File)
		}
	}

	// 4. Empty pattern error
	_, err = GrepWorkspace(tempDir, GrepOptions{
		Pattern: "   ",
	})
	if err != ErrPatternEmpty {
		t.Errorf("expected ErrPatternEmpty, got: %v", err)
	}

	// 5. Jail escape error
	_, err = GrepWorkspace(tempDir, GrepOptions{
		Pattern: "test",
		Path:    "../../etc",
	})
	if err != ErrPathEscapesJail {
		t.Errorf("expected ErrPathEscapesJail, got: %v", err)
	}
}

func TestApplyPatch_SuccessAndDryRun(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-patch-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	targetFile := filepath.Join(tempDir, "sample.txt")
	originalContent := "line 1\nline 2\nline 3\nline 4\n"
	if err := os.WriteFile(targetFile, []byte(originalContent), 0644); err != nil {
		t.Fatalf("failed writing sample file: %v", err)
	}

	patchDiff := `--- a/sample.txt
+++ b/sample.txt
@@ -1,4 +1,5 @@
 line 1
-line 2
+line 2 patched
+line 2.5 new
 line 3
 line 4
`

	// 1. Dry Run
	dryRes, err := ApplyPatch(tempDir, patchDiff, true)
	if err != nil {
		t.Fatalf("dry run patch failed: %v", err)
	}
	if !dryRes.DryRun || dryRes.HunksApplied != 1 || dryRes.LinesAdded != 2 || dryRes.LinesDeleted != 1 {
		t.Fatalf("unexpected dry run result: %+v", dryRes)
	}

	// Verify file was NOT modified in dry run
	currentContent, _ := os.ReadFile(targetFile)
	if string(currentContent) != originalContent {
		t.Errorf("dry run modified the file: %q", string(currentContent))
	}

	// 2. Real Apply
	realRes, err := ApplyPatch(tempDir, patchDiff, false)
	if err != nil {
		t.Fatalf("real apply patch failed: %v", err)
	}
	if realRes.DryRun || realRes.Status != "applied" {
		t.Fatalf("unexpected real apply result: %+v", realRes)
	}

	// Verify file was patched
	updatedContent, _ := os.ReadFile(targetFile)
	expectedUpdated := "line 1\nline 2 patched\nline 2.5 new\nline 3\nline 4\n"
	if string(updatedContent) != expectedUpdated {
		t.Errorf("patched content mismatch:\nexpected:\n%s\ngot:\n%s", expectedUpdated, string(updatedContent))
	}
}

func TestApplyPatch_Failures(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-patch-fail-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	targetFile := filepath.Join(tempDir, "sample.txt")
	_ = os.WriteFile(targetFile, []byte("hello world\n"), 0644)

	// Context mismatch
	badContextPatch := `--- a/sample.txt
+++ b/sample.txt
@@ -1,1 +1,1 @@
-nonexistent line
+new line
`
	_, err = ApplyPatch(tempDir, badContextPatch, false)
	if !errors.Is(err, ErrHunkMatchFailure) {
		t.Errorf("expected ErrHunkMatchFailure, got: %v", err)
	}

	// Missing target file in header
	noHeaderPatch := `@@ -1,1 +1,1 @@
-hello world
+hello vessel
`
	_, err = ApplyPatch(tempDir, noHeaderPatch, false)
	if !errors.Is(err, ErrNoTargetFile) {
		t.Errorf("expected ErrNoTargetFile, got: %v", err)
	}

	// Jail escape target
	jailEscapePatch := `--- a/../../etc/passwd
+++ b/../../etc/passwd
@@ -1,1 +1,1 @@
-root
+hacked
`
	_, err = ApplyPatch(tempDir, jailEscapePatch, false)
	if !errors.Is(err, ErrPathEscapesJail) {
		t.Errorf("expected ErrPathEscapesJail, got: %v", err)
	}
}

func TestSearchPatch_PersonaQA(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-searchpatch-persona-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 1. Tier 1: Monkey - Binary file handling (null bytes in file)
	binFile := filepath.Join(tempDir, "image.bin")
	_ = os.WriteFile(binFile, []byte("GIF89a\x00\x01\x00find_me_in_binary"), 0644)
	res, err := GrepWorkspace(tempDir, GrepOptions{Pattern: "find_me"})
	if err != nil {
		t.Fatalf("Grep on binary dir failed: %v", err)
	}
	if len(res.Matches) != 0 {
		t.Errorf("binary file matches should be skipped, got: %+v", res.Matches)
	}

	// 2. Tier 2: Blind Box - Invalid regex pattern
	_, err = GrepWorkspace(tempDir, GrepOptions{Pattern: "[unclosed-bracket"})
	if err == nil {
		t.Errorf("expected regex compilation error for unclosed bracket, got nil")
	}

	// 3. Tier 3: Semi-Tech - Max match truncation
	textPath := filepath.Join(tempDir, "repeats.txt")
	_ = os.WriteFile(textPath, []byte("match 1\nmatch 2\nmatch 3\nmatch 4\n"), 0644)
	truncRes, err := GrepWorkspace(tempDir, GrepOptions{Pattern: "match", MaxMatches: 2})
	if err != nil {
		t.Fatalf("Grep with limit failed: %v", err)
	}
	if !truncRes.Truncated || len(truncRes.Matches) != 2 {
		t.Errorf("expected Truncated=true and exactly 2 matches, got len=%d, trunc=%v", len(truncRes.Matches), truncRes.Truncated)
	}

	// 4. Tier 4: Adversarial - Multi-hunk patch with line offset shifting
	multiDoc := filepath.Join(tempDir, "multi.txt")
	_ = os.WriteFile(multiDoc, []byte("lineA\nlineB\nlineC\nlineD\nlineE\nlineF\n"), 0644)

	multiPatch := `--- a/multi.txt
+++ b/multi.txt
@@ -1,3 +1,4 @@
 lineA
+lineA.1
 lineB
 lineC
@@ -4,3 +5,3 @@
 lineD
-lineE
+lineE_modified
 lineF
`
	patchRes, err := ApplyPatch(tempDir, multiPatch, false)
	if err != nil {
		t.Fatalf("multi-hunk patch failed: %v", err)
	}
	if patchRes.HunksApplied != 2 || patchRes.LinesAdded != 2 || patchRes.LinesDeleted != 1 {
		t.Errorf("unexpected multi-hunk patch result: %+v", patchRes)
	}

	finalBytes, _ := os.ReadFile(multiDoc)
	expectedFinal := "lineA\nlineA.1\nlineB\nlineC\nlineD\nlineE_modified\nlineF\n"
	if string(finalBytes) != expectedFinal {
		t.Errorf("multi-hunk output mismatch:\nexpected:\n%s\ngot:\n%s", expectedFinal, string(finalBytes))
	}
}
