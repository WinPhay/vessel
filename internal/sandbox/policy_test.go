package sandbox

import (
	"errors"
	"testing"
)

func TestPathPolicy_ReadOnly(t *testing.T) {
	policy := &PathPolicy{
		ReadOnly: true,
	}

	// Reading allowed
	if err := policy.CheckRead("src/main.go"); err != nil {
		t.Errorf("expected read permitted, got: %v", err)
	}

	// Writing blocked
	if err := policy.CheckWrite("src/main.go"); !errors.Is(err, ErrWorkspaceReadOnly) {
		t.Errorf("expected ErrWorkspaceReadOnly, got: %v", err)
	}
}

func TestPathPolicy_ROPaths(t *testing.T) {
	policy := &PathPolicy{
		ROPaths: []string{
			".env",
			".git",
			"config/*.yaml",
			"vendor/**",
		},
	}

	// Normal file write allowed
	if err := policy.CheckWrite("src/app.go"); err != nil {
		t.Errorf("expected write permitted for src/app.go, got: %v", err)
	}

	// Exact RO path blocked
	if err := policy.CheckWrite(".env"); !errors.Is(err, ErrPathReadOnly) {
		t.Errorf("expected ErrPathReadOnly for .env, got: %v", err)
	}
	if err := policy.CheckWrite("/workspace/.env"); !errors.Is(err, ErrPathReadOnly) {
		t.Errorf("expected ErrPathReadOnly for /workspace/.env, got: %v", err)
	}

	// Directory prefix inheritance blocked
	if err := policy.CheckWrite(".git/HEAD"); !errors.Is(err, ErrPathReadOnly) {
		t.Errorf("expected ErrPathReadOnly for .git/HEAD, got: %v", err)
	}
	if err := policy.CheckWrite(".git/refs/heads/main"); !errors.Is(err, ErrPathReadOnly) {
		t.Errorf("expected ErrPathReadOnly for .git/refs/heads/main, got: %v", err)
	}

	// Glob match blocked
	if err := policy.CheckWrite("config/db.yaml"); !errors.Is(err, ErrPathReadOnly) {
		t.Errorf("expected ErrPathReadOnly for config/db.yaml, got: %v", err)
	}
	// Non-matching file in config allowed
	if err := policy.CheckWrite("config/db.json"); err != nil {
		t.Errorf("expected write permitted for config/db.json, got: %v", err)
	}

	// Recursive double-asterisk glob blocked
	if err := policy.CheckWrite("vendor/github.com/pkg/errors/errors.go"); !errors.Is(err, ErrPathReadOnly) {
		t.Errorf("expected ErrPathReadOnly for vendor path, got: %v", err)
	}
}

func TestPathPolicy_DenyPaths(t *testing.T) {
	policy := &PathPolicy{
		DenyPaths: []string{
			".secrets",
			"*.pem",
			"private/**",
		},
	}

	// Normal file read/write allowed
	if err := policy.CheckRead("public/index.html"); err != nil {
		t.Errorf("expected read permitted, got: %v", err)
	}
	if err := policy.CheckWrite("public/index.html"); err != nil {
		t.Errorf("expected write permitted, got: %v", err)
	}

	// Denied file read & write blocked
	if err := policy.CheckRead(".secrets"); !errors.Is(err, ErrPathDenied) {
		t.Errorf("expected ErrPathDenied for reading .secrets, got: %v", err)
	}
	if err := policy.CheckWrite(".secrets"); !errors.Is(err, ErrPathDenied) {
		t.Errorf("expected ErrPathDenied for writing .secrets, got: %v", err)
	}

	// Denied directory prefix blocked
	if err := policy.CheckRead(".secrets/api_key.txt"); !errors.Is(err, ErrPathDenied) {
		t.Errorf("expected ErrPathDenied for reading .secrets child, got: %v", err)
	}

	// Wildcard .pem blocked
	if err := policy.CheckRead("certs/server.pem"); !errors.Is(err, ErrPathDenied) {
		t.Errorf("expected ErrPathDenied for certs/server.pem, got: %v", err)
	}

	// FilterEntries
	entries := []DirEntry{
		{Name: "index.html", Path: "public/index.html"},
		{Name: "server.pem", Path: "certs/server.pem"},
		{Name: "api_key.txt", Path: ".secrets/api_key.txt"},
		{Name: "styles.css", Path: "public/styles.css"},
	}
	filtered := policy.FilterEntries(entries)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered entries, got %d", len(filtered))
	}
	if filtered[0].Name != "index.html" || filtered[1].Name != "styles.css" {
		t.Errorf("unexpected filtered entries: %+v", filtered)
	}
}
