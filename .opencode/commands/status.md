# /status — Session Verification Command

Run this command before committing or declaring work completed:

1. Format check:
   ```bash
   go fmt ./...
   ```
2. Static analysis and vetting:
   ```bash
   go vet ./...
   ```
3. Unit test suite:
   ```bash
   go test -v ./...
   ```
4. Git status inspection:
   ```bash
   git status --short
   ```
All gates must report exit code 0.
