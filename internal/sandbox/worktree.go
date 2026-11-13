package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WinPhay/vessel/internal/tokenfilter"
)

// WorktreeStatusResult holds Git worktree status and file modification tracking.
type WorktreeStatusResult struct {
	IsWorktree     bool     `json:"is_worktree"`
	Branch         string   `json:"branch"`
	BaseBranch     string   `json:"base_branch"`
	WorktreePath   string   `json:"worktree_path"`
	ModifiedFiles  []string `json:"modified_files"`
	UntrackedFiles []string `json:"untracked_files"`
	Clean          bool     `json:"clean"`
}

// WorktreeDiffResult holds the unified diff of changes made within the worktree.
type WorktreeDiffResult struct {
	Diff          string `json:"diff"`
	FilesChanged  int    `json:"files_changed"`
	Additions     int    `json:"additions"`
	Deletions     int    `json:"deletions"`
	TokenEstimate int    `json:"token_estimate"`
	Truncated     bool   `json:"truncated"`
}

// WorktreeCommitResult holds details of a commit created in the worktree.
type WorktreeCommitResult struct {
	CommitHash string `json:"commit_hash"`
	Branch     string `json:"branch"`
	Message    string `json:"message"`
	FilesCount int    `json:"files_count"`
}

// WorktreeManager oversees the lifecycle of an isolated Git worktree for an execution session.
type WorktreeManager struct {
	repoRoot     string
	worktreePath string
	branchName   string
	baseBranch   string
	isGitRepo    bool
	isWorktree   bool
	mu           sync.RWMutex
	closed       bool
}

// IsGitRepo returns true if path is located inside a Git working tree.
func IsGitRepo(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// NewWorktreeManager initializes and creates an isolated Git worktree under .vessel/worktrees/.
func NewWorktreeManager(repoRoot, sessionID string) (*WorktreeManager, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		absRoot = repoRoot
	}

	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	if !IsGitRepo(absRoot) {
		return &WorktreeManager{
			repoRoot:     absRoot,
			worktreePath: absRoot,
			isGitRepo:    false,
			isWorktree:   false,
		}, nil
	}

	// Detect current branch / HEAD
	cmdBase := exec.Command("git", "-C", absRoot, "rev-parse", "--abbrev-ref", "HEAD")
	baseOut, err := cmdBase.Output()
	baseBranch := "main"
	if err == nil && strings.TrimSpace(string(baseOut)) != "" {
		baseBranch = strings.TrimSpace(string(baseOut))
	}

	branchName := fmt.Sprintf("vessel/session-%s", sessionID)
	worktreeDir := filepath.Join(absRoot, ".vessel", "worktrees", fmt.Sprintf("vessel-%s", sessionID))

	// Ensure .vessel is ignored in Git exclude to prevent index noise
	excludePath := filepath.Join(absRoot, ".git", "info", "exclude")
	if excludeData, err := os.ReadFile(excludePath); err == nil {
		if !strings.Contains(string(excludeData), ".vessel") {
			_ = os.WriteFile(excludePath, append(excludeData, []byte("\n.vessel\n")...), 0644)
		}
	}

	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0755); err != nil {
		return nil, fmt.Errorf("failed to create worktree parent dir: %w", err)
	}

	// Create git worktree
	cmdAdd := exec.Command("git", "-C", absRoot, "worktree", "add", "-b", branchName, worktreeDir, "HEAD")
	if out, err := cmdAdd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git worktree add failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	return &WorktreeManager{
		repoRoot:     absRoot,
		worktreePath: worktreeDir,
		branchName:   branchName,
		baseBranch:   baseBranch,
		isGitRepo:    true,
		isWorktree:   true,
	}, nil
}

// WorktreePath returns the active filesystem path for container mounting.
func (wm *WorktreeManager) WorktreePath() string {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.worktreePath
}

// IsWorktree returns true if worktree isolation is active.
func (wm *WorktreeManager) IsWorktree() bool {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.isWorktree
}

// BranchName returns the isolated worktree branch name.
func (wm *WorktreeManager) BranchName() string {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.branchName
}

// Status returns the current working tree modifications.
func (wm *WorktreeManager) Status(ctx context.Context) (*WorktreeStatusResult, error) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	if wm.closed {
		return nil, ErrSandboxClosed
	}

	if !wm.isGitRepo {
		return &WorktreeStatusResult{
			IsWorktree:   false,
			WorktreePath: wm.worktreePath,
			Clean:        true,
		}, nil
	}

	cmd := exec.CommandContext(ctx, "git", "-C", wm.worktreePath, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status failed: %w", err)
	}

	var modified, untracked []string
	lines := strings.Split(string(out), "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if len(trimmed) < 3 {
			continue
		}
		status := trimmed[:2]
		file := strings.TrimSpace(trimmed[2:])
		if status == "??" {
			untracked = append(untracked, file)
		} else {
			modified = append(modified, file)
		}
	}

	return &WorktreeStatusResult{
		IsWorktree:     wm.isWorktree,
		Branch:         wm.branchName,
		BaseBranch:     wm.baseBranch,
		WorktreePath:   wm.worktreePath,
		ModifiedFiles:  modified,
		UntrackedFiles: untracked,
		Clean:          len(modified) == 0 && len(untracked) == 0,
	}, nil
}

// Diff returns the unified diff of changes made within the worktree against HEAD.
func (wm *WorktreeManager) Diff(ctx context.Context, maxBytes int) (*WorktreeDiffResult, error) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	if wm.closed {
		return nil, ErrSandboxClosed
	}

	if !wm.isGitRepo {
		return &WorktreeDiffResult{
			Diff:          "",
			TokenEstimate: 0,
		}, nil
	}

	cmd := exec.CommandContext(ctx, "git", "-C", wm.worktreePath, "diff", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	rawDiff := string(out)

	// Calculate line metrics
	filesChanged := 0
	additions := 0
	deletions := 0

	lines := strings.Split(rawDiff, "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "diff --git") {
			filesChanged++
		} else if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
			additions++
		} else if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---") {
			deletions++
		}
	}

	cleaned := tokenfilter.CleanOutput(rawDiff)
	truncated := false
	if maxBytes > 0 && len(cleaned) > maxBytes {
		cleaned, truncated = tokenfilter.TruncateOutput(cleaned, maxBytes)
	}

	return &WorktreeDiffResult{
		Diff:          cleaned,
		FilesChanged:  filesChanged,
		Additions:     additions,
		Deletions:     deletions,
		TokenEstimate: tokenfilter.EstimateTokens(cleaned),
		Truncated:     truncated,
	}, nil
}

// Commit stages all changes and creates a Git commit on the worktree branch.
func (wm *WorktreeManager) Commit(ctx context.Context, message, authorName, authorEmail string) (*WorktreeCommitResult, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if wm.closed {
		return nil, ErrSandboxClosed
	}

	if !wm.isGitRepo {
		return nil, fmt.Errorf("workspace is not a git repository")
	}

	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("commit message cannot be empty")
	}

	// Stage all changes
	cmdAdd := exec.CommandContext(ctx, "git", "-C", wm.worktreePath, "add", "-A")
	if out, err := cmdAdd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git add failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	// Commit
	commitArgs := []string{"-C", wm.worktreePath, "commit", "-m", message}
	if authorName != "" && authorEmail != "" {
		commitArgs = append(commitArgs, fmt.Sprintf("--author=%s <%s>", authorName, authorEmail))
	}

	cmdCommit := exec.CommandContext(ctx, "git", commitArgs...)
	if out, err := cmdCommit.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git commit failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	// Get latest commit hash
	cmdRev := exec.CommandContext(ctx, "git", "-C", wm.worktreePath, "rev-parse", "HEAD")
	revOut, err := cmdRev.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get commit hash: %w", err)
	}

	// Count committed files
	cmdStat := exec.CommandContext(ctx, "git", "-C", wm.worktreePath, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD")
	statOut, _ := cmdStat.Output()
	filesCount := 0
	for _, f := range strings.Split(strings.TrimSpace(string(statOut)), "\n") {
		if strings.TrimSpace(f) != "" {
			filesCount++
		}
	}

	return &WorktreeCommitResult{
		CommitHash: strings.TrimSpace(string(revOut)),
		Branch:     wm.branchName,
		Message:    message,
		FilesCount: filesCount,
	}, nil
}

// Discard forces removal of the ephemeral worktree and its isolated branch.
func (wm *WorktreeManager) Discard(ctx context.Context) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if !wm.isWorktree || wm.closed {
		return nil
	}
	wm.closed = true

	// Remove worktree
	cmdRm := exec.CommandContext(ctx, "git", "-C", wm.repoRoot, "worktree", "remove", "--force", wm.worktreePath)
	_ = cmdRm.Run()

	// Delete branch
	if wm.branchName != "" {
		cmdBranch := exec.CommandContext(ctx, "git", "-C", wm.repoRoot, "branch", "-D", wm.branchName)
		_ = cmdBranch.Run()
	}

	_ = os.RemoveAll(wm.worktreePath)
	return nil
}

// Close cleans up worktree if it was marked ephemeral.
func (wm *WorktreeManager) Close() error {
	return wm.Discard(context.Background())
}

// ParseDiffMetrics parses files changed, insertions, and deletions from standard git diff stat text.
func ParseDiffMetrics(statText string) (int, int, int) {
	// e.g., " 3 files changed, 10 insertions(+), 2 deletions(-)"
	var files, adds, dels int
	parts := strings.Split(statText, ",")
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if strings.Contains(trimmed, "file") {
			fields := strings.Fields(trimmed)
			if len(fields) > 0 {
				files, _ = strconv.Atoi(fields[0])
			}
		} else if strings.Contains(trimmed, "insertion") {
			fields := strings.Fields(trimmed)
			if len(fields) > 0 {
				adds, _ = strconv.Atoi(fields[0])
			}
		} else if strings.Contains(trimmed, "deletion") {
			fields := strings.Fields(trimmed)
			if len(fields) > 0 {
				dels, _ = strconv.Atoi(fields[0])
			}
		}
	}
	return files, adds, dels
}
