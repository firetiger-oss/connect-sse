---
description: Cut a new release of connect-sse — tag, push, and verify the release workflow.
---

# Release

1. Confirm CI is green on `main`:
   ```bash
   gh run list --workflow=ci.yml --branch=main --limit=1
   ```
2. Decide the version bump using semver. Wire-format changes (SSE framing, compression negotiation, error frame shape) are major. Public API changes in `client.go` / `server.go` are major. Internal-only changes are minor or patch.
3. Tag and push:
   ```bash
   git checkout main && git pull
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```
4. Watch the release workflow until it finishes:
   ```bash
   gh run watch $(gh run list --workflow=release.yml --limit=1 --json databaseId --jq '.[0].databaseId')
   ```
5. Verify on pkg.go.dev that the new version appears (proxy can take a few minutes):
   ```bash
   open "https://pkg.go.dev/github.com/firetiger-oss/connect-sse@vX.Y.Z"
   ```
