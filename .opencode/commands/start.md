# /start — Session Startup Command

Run this command at the beginning of every development turn in `vessel`:

1. Check environment and toolchain:
   ```bash
   go version
   git status
   ```
2. Verify all tests pass on current branch:
   ```bash
   go test ./... && go vet ./...
   ```
3. Report branch name, working tree cleanliness, and readiness status.
