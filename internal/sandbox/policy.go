package sandbox

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

var (
	// ErrWorkspaceReadOnly indicates the entire workspace is mounted read-only.
	ErrWorkspaceReadOnly = errors.New("workspace is mounted in read-only mode")
	// ErrPathReadOnly indicates a specific path or pattern is protected by read-only policy.
	ErrPathReadOnly = errors.New("path is protected by read-only policy")
	// ErrPathDenied indicates access to a specific path is forbidden by security policy.
	ErrPathDenied = errors.New("path access is denied by security policy")
)

// PathPolicy defines fine-grained access control and mount constraints for workspace files.
type PathPolicy struct {
	ReadOnly  bool     `json:"read_only"`
	ROPaths   []string `json:"ro_paths,omitempty"`
	DenyPaths []string `json:"deny_paths,omitempty"`
}

// NormalizePolicyPath normalizes a user or pattern path into a clean, relative, slash-separated path.
func NormalizePolicyPath(rawPath string) string {
	cleaned := strings.TrimSpace(rawPath)
	cleaned = filepath.ToSlash(cleaned)
	cleaned = strings.TrimPrefix(cleaned, "/workspace")
	cleaned = strings.TrimPrefix(cleaned, "workspace")
	cleaned = strings.TrimPrefix(cleaned, "/")
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = path.Clean(cleaned)
	if cleaned == "." {
		return ""
	}
	return cleaned
}

// matchPattern checks if a target relative path matches a policy pattern.
// Supports exact matching, directory prefix inheritance, and standard glob wildcards.
func matchPattern(pattern, relPath string) bool {
	normPat := NormalizePolicyPath(pattern)
	normTarget := NormalizePolicyPath(relPath)

	if normPat == "" || normTarget == "" {
		if normPat == normTarget {
			return true
		}
		return false
	}

	// Exact match
	if normPat == normTarget {
		return true
	}

	// Directory prefix match: if pattern is a directory, all children inherit the rule
	if strings.HasPrefix(normTarget, normPat+"/") {
		return true
	}

	// Standard glob match (e.g. *.key, .env*)
	matched, err := path.Match(normPat, normTarget)
	if err == nil && matched {
		return true
	}

	// Base name glob match (e.g. *.env matching dir/sub.env)
	baseTarget := path.Base(normTarget)
	matchedBase, err := path.Match(normPat, baseTarget)
	if err == nil && matchedBase {
		return true
	}

	// Double asterisk recursive glob (e.g. **/*.secret)
	if strings.Contains(normPat, "**") {
		parts := strings.Split(normPat, "**")
		if len(parts) == 2 {
			prefix := strings.TrimSuffix(parts[0], "/")
			suffix := strings.TrimPrefix(parts[1], "/")
			hasPrefix := prefix == "" || strings.HasPrefix(normTarget, prefix)
			hasSuffix := suffix == "" || strings.HasSuffix(normTarget, suffix)
			if hasPrefix && hasSuffix {
				return true
			}
		}
	}

	return false
}

// MatchesAny checks if target relative path matches any pattern in the list.
func (p *PathPolicy) MatchesAny(patterns []string, userPath string) bool {
	rel := NormalizePolicyPath(userPath)
	for _, pat := range patterns {
		if matchPattern(pat, rel) {
			return true
		}
	}
	return false
}

// CheckRead verifies if reading userPath is permitted under the policy.
func (p *PathPolicy) CheckRead(userPath string) error {
	if p == nil {
		return nil
	}
	if p.MatchesAny(p.DenyPaths, userPath) {
		return ErrPathDenied
	}
	return nil
}

// CheckWrite verifies if modifying userPath is permitted under the policy.
func (p *PathPolicy) CheckWrite(userPath string) error {
	if p == nil {
		return nil
	}
	if p.ReadOnly {
		return ErrWorkspaceReadOnly
	}
	if p.MatchesAny(p.DenyPaths, userPath) {
		return ErrPathDenied
	}
	if p.MatchesAny(p.ROPaths, userPath) {
		return ErrPathReadOnly
	}
	return nil
}

// FilterEntries removes any directory entries whose paths match the DenyPaths policy.
func (p *PathPolicy) FilterEntries(entries []DirEntry) []DirEntry {
	if p == nil || len(p.DenyPaths) == 0 {
		return entries
	}
	filtered := make([]DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !p.MatchesAny(p.DenyPaths, entry.Path) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
