---
description: Test in-progress connect-sse changes against firetiger-oss/connect-aip without publishing a tag.
---

# Try in connect-aip

When a change here affects the public API or wire format, validate it in connect-aip (the primary downstream) before tagging.

1. Check out connect-aip alongside this repo (sibling directory):
   ```bash
   git -C ../connect-aip status
   ```
2. Add a `replace` directive pointing connect-aip's go.mod at this checkout:
   ```bash
   cd ../connect-aip
   go mod edit -replace=github.com/firetiger-oss/connect-sse=../connect-sse
   ```
3. Run connect-aip's tests:
   ```bash
   go test -race ./...
   ```
4. If you regenerated test fixtures or made codegen changes, also run:
   ```bash
   go install ./cmd/...
   buf generate internal/testproto
   ```
5. Roll back the replace before opening a PR in connect-aip:
   ```bash
   go mod edit -dropreplace=github.com/firetiger-oss/connect-sse
   ```

Cross-references between firetiger-oss repos are fine. Do not point a `replace` directive at any private repository.
