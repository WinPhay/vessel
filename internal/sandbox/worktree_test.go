package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func createTestGitRepo(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "vessel-git-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=TestAuthor",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=TestAuthor",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git command failed: %s (%v)", string(out), err)
		}
	}

	runGit("init", "-b", "main")
	runGit("config", "user.name", "TestAuthor")
	runGit("config", "user.email", "test@example.com")

	initialFile := filepath.Join(dir, "README.md")
	if err := os.WriteFile(initialFile, []byte("# Hello Git\n"), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	runGit("add", "README.md")
	runGit("commit", "-m", "initial commit")

	return dir
}

func TestWorktreeManager_Lifecycle(t *testing.T) {
	repoDir := createTestGitRepo(t)
	defer os.RemoveAll(repoDir)

	ctx := context.Background()
	wm, err := NewWorktreeManager(repoDir, "test-session")
	if err != nil {
		t.Fatalf("failed to initialize WorktreeManager: %v", err)
	}
	defer func() { _ = wm.Close() }()

	if !wm.IsWorktree() {
		t.Fatalf("expected worktree to be active")
	}

	expectedBranch := "vessel/session-test-session"
	if wm.BranchName() != expectedBranch {
		t.Errorf("expected branch %s, got %s", expectedBranch, wm.BranchName())
	}

	// 1. Initial status should be clean
	status, err := wm.Status(ctx)
	if err != nil {
		t.Fatalf("failed to get status: %v", err)
	}
	if !status.Clean {
		t.Errorf("expected clean initial status, got: %+v", status)
	}

	// 2. Mutate file in worktree
	worktreeFile := filepath.Join(wm.WorktreePath(), "README.md")
	if err := os.WriteFile(worktreeFile, []byte("# Hello Git\nModified line by agent\n"), 0644); err != nil {
		t.Fatalf("failed to mutate worktree file: %v", err)
	}

	// 3. Status should detect modified file
	status, err = wm.Status(ctx)
	if err != nil {
		t.Fatalf("failed to get status after edit: %v", err)
	}
	if status.Clean || len(status.ModifiedFiles) != 1 || status.ModifiedFiles[0] != "README.md" {
		t.Errorf("unexpected status after edit: %+v", status)
	}

	// 4. Diff should show additions
	diffRes, err := wm.Diff(ctx, 0)
	if err != nil {
		t.Fatalf("failed to get diff: %v", err)
	}
	if !strings.Contains(diffRes.Diff, "Modified line by agent") {
		t.Errorf("expected diff to contain added line, got:\n%s", diffRes.Diff)
	}
	if diffRes.FilesChanged != 1 || diffRes.Additions != 1 {
		t.Errorf("unexpected diff metrics: changed=%d, adds=%d, dels=%d", diffRes.FilesChanged, diffRes.Additions, diffRes.Deletions)
	}

	// 5. Host repository primary tree MUST remain clean and untouched
	cmdHost := exec.Command("git", "-C", repoDir, "status", "--porcelain")
	hostOut, err := cmdHost.Output()
	if err != nil || len(strings.TrimSpace(string(hostOut))) != 0 {
		t.Errorf("CRITICAL: host primary working tree was contaminated: %s", string(hostOut))
	}

	// 6. Commit inside worktree
	commitRes, err := wm.Commit(ctx, "feat: agent modifications", "Agent", "agent@vessel.local")
	if err != nil {
		t.Fatalf("failed to commit worktree: %v", err)
	}
	if commitRes.CommitHash == "" || commitRes.FilesCount != 1 {
		t.Errorf("unexpected commit result: %+v", commitRes)
	}

	// 7. Worktree should now be clean
	status, _ = wm.Status(ctx)
	if !status.Clean {
		t.Errorf("expected clean status post-commit, got: %+v", status)
	}

	// 8. Discard and clean up
	if err := wm.Discard(ctx); err != nil {
		t.Errorf("failed to discard worktree: %v", err)
	}
	if _, err := os.Stat(wm.WorktreePath()); !os.IsNotExist(err) {
		t.Errorf("expected worktree directory to be removed after discard")
	}
}

func TestWorktreeManager_NonGitFallback(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vessel-nongit-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx := context.Background()
	wm, err := NewWorktreeManager(tempDir, "fallback")
	if err != nil {
		t.Fatalf("failed to init on non-git dir: %v", err)
	}
	defer func() { _ = wm.Close() }()

	if wm.IsWorktree() {
		t.Errorf("expected IsWorktree to be false for non-git dir")
	}

	status, err := wm.Status(ctx)
	if err != nil || !status.Clean {
		t.Errorf("unexpected non-git status: %+v", status)
	}
}

func TestWorktree_PersonaQA(t *testing.T) {
	ctx := context.Background()

	// =========================================================================
	// TIER 1: The Monkey / Fuzz Tester
	// Empty commit message, double close, diff truncation
	// =========================================================================
	t.Run("Tier1_Monkey_Chaos", func(t *testing.T) {
		repoDir := createTestGitRepo(t)
		defer os.RemoveAll(repoDir)

		wm, _ := NewWorktreeManager(repoDir, "tier1-session")
		defer func() { _ = wm.Close() }()

		// 1. Empty commit message must fail
		_, err := wm.Commit(ctx, "", "Author", "author@vessel.local")
		if err == nil {
			t.Errorf("Tier 1: expected error on empty commit message")
		}

		// 2. Whitespace-only commit message must fail
		_, err = wm.Commit(ctx, "   \n\t  ", "Author", "author@vessel.local")
		if err == nil {
			t.Errorf("Tier 1: expected error on whitespace-only commit message")
		}

		// 3. Diff truncation with maxBytes
		newFile := filepath.Join(wm.WorktreePath(), "huge.txt")
		_ = os.WriteFile(newFile, []byte(strings.Repeat("Line of text for diff truncation\n", 50)), 0644)
		cmdAdd := exec.Command("git", "-C", wm.WorktreePath(), "add", "huge.txt")
		_ = cmdAdd.Run()
		diffRes, err := wm.Diff(ctx, 100)
		if err != nil {
			t.Fatalf("Tier 1: diff failed: %v", err)
		}
		if !diffRes.Truncated {
			t.Errorf("Tier 1: expected diff to be marked truncated")
		}
		if !strings.Contains(diffRes.Diff, "TRUNCATED") {
			t.Errorf("Tier 1: expected truncation banner in diff")
		}

		// 4. Double discard must not panic or error
		_ = wm.Discard(ctx)
		_ = wm.Discard(ctx)
	})

	// =========================================================================
	// TIER 2: The Blind Box / Accidental Developer
	// Untracked files, deletions, new directories
	// =========================================================================
	t.Run("Tier2_BlindBox_ComplexChanges", func(t *testing.T) {
		repoDir := createTestGitRepo(t)
		defer os.RemoveAll(repoDir)

		wm, _ := NewWorktreeManager(repoDir, "tier2-session")
		defer func() { _ = wm.Close() }()

		// Create nested untracked file
		subDir := filepath.Join(wm.WorktreePath(), "src", "components")
		_ = os.MkdirAll(subDir, 0755)
		_ = os.WriteFile(filepath.Join(subDir, "Button.tsx"), []byte("export const Button = () => <button/>;\n"), 0644)

		status, err := wm.Status(ctx)
		if err != nil {
			t.Fatalf("Tier 2: status failed: %v", err)
		}
		if status.Clean {
			t.Errorf("Tier 2: status should not be clean with untracked files")
		}
		foundUntracked := false
		for _, u := range status.UntrackedFiles {
			if strings.Contains(u, "Button.tsx") || strings.Contains(u, "src/") {
				foundUntracked = true
				break
			}
		}
		if !foundUntracked {
			t.Errorf("Tier 2: expected Button.tsx in untracked files, got: %+v", status.UntrackedFiles)
		}

		// Commit all changes
		commitRes, err := wm.Commit(ctx, "feat(ui): add Button component", "Dev", "dev@vessel.local")
		if err != nil {
			t.Fatalf("Tier 2: commit failed: %v", err)
		}
		if commitRes.FilesCount < 1 {
			t.Errorf("Tier 2: expected files count >= 1, got %d", commitRes.FilesCount)
		}
	})

	// =========================================================================
	// TIER 3: The Semi-Tech Agent
	// ParseDiffMetrics helper tests, git exclude validation
	// =========================================================================
	t.Run("Tier3_SemiTech_MetricsAndExcludes", func(t *testing.T) {
		files, adds, dels := ParseDiffMetrics(" 3 files changed, 45 insertions(+), 12 deletions(-)")
		if files != 3 || adds != 45 || dels != 12 {
			t.Errorf("Tier 3: ParseDiffMetrics mismatch: files=%d, adds=%d, dels=%d", files, adds, dels)
		}

		repoDir := createTestGitRepo(t)
		defer os.RemoveAll(repoDir)

		wm, _ := NewWorktreeManager(repoDir, "tier3-session")
		defer func() { _ = wm.Close() }()

		// Verify .vessel is excluded in .git/info/exclude
		excludePath := filepath.Join(repoDir, ".git", "info", "exclude")
		data, err := os.ReadFile(excludePath)
		if err != nil || !strings.Contains(string(data), ".vessel") {
			t.Errorf("Tier 3: .vessel not found in .git/info/exclude: %s", string(data))
		}
	})

	// =========================================================================
	// TIER 4: The Adversarial Adversary
	// Verify host working tree 100% shielded from destructive agent operations
	// =========================================================================
	t.Run("Tier4_Adversarial_ZeroHostContamination", func(t *testing.T) {
		repoDir := createTestGitRepo(t)
		defer os.RemoveAll(repoDir)

		// Create an unstaged file on the host BEFORE launching agent
		hostUnstaged := filepath.Join(repoDir, "uncommitted_work.txt")
		_ = os.WriteFile(hostUnstaged, []byte("developer half-written code"), 0644)

		wm, _ := NewWorktreeManager(repoDir, "tier4-adversary")

		// Agent aggressively writes to README.md and creates files inside worktree
		_ = os.WriteFile(filepath.Join(wm.WorktreePath(), "README.md"), []byte("AGENT OVERWRITE!"), 0644)
		_ = os.WriteFile(filepath.Join(wm.WorktreePath(), "agent_secret.txt"), []byte("agent created file"), 0644)

		// Agent commits work inside worktree
		_, err := wm.Commit(ctx, "agent: overwrite readme", "Agent", "agent@local")
		if err != nil {
			t.Fatalf("Tier 4: agent commit failed: %v", err)
		}

		// Agent finishes and worktree is discarded
		_ = wm.Discard(ctx)

		// VERIFICATION: The host's original README.md must be unmodified!
		hostReadme, _ := os.ReadFile(filepath.Join(repoDir, "README.md"))
		if string(hostReadme) != "# Hello Git\n" {
			t.Fatalf("CRITICAL SECURITY INVARIANT VIOLATION: host README.md was overwritten! Content: %s", string(hostReadme))
		}

		// VERIFICATION: The host's unstaged work must still exist!
		hostUnstagedData, err := os.ReadFile(hostUnstaged)
		if err != nil || string(hostUnstagedData) != "developer half-written code" {
			t.Fatalf("CRITICAL SECURITY INVARIANT VIOLATION: host uncommitted work was destroyed!")
		}
	})
}
