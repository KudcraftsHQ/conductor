---
name: conductor-pipeline
description: Full development pipeline using Conductor worktrees + TrustLayer spec-driven TDD + ProofShot visual verification. Activate when user says "verify", "run pipeline", "test my changes", or after implementing a feature in a Conductor-managed worktree.
---

# Conductor Pipeline

Orchestrates the full development pipeline for Conductor-managed projects, combining:
- **Conductor**: worktree management with isolated ports
- **TrustLayer**: spec-driven TDD (build, review, break)
- **ProofShot**: visual verification with video proof + screenshots

## Context Detection

Read environment variables to determine what tools are available:

```bash
echo "Framework: $CONDUCTOR_FRAMEWORK"
echo "Web eligible: $CONDUCTOR_WEB_ELIGIBLE"
echo "TrustLayer: $CONDUCTOR_TRUSTLAYER"
echo "ProofShot port: $PROOFSHOT_PORT"
echo "ProofShot run cmd: $PROOFSHOT_RUN_CMD"
```

Or read `conductor.json` in the project root for the `tooling` section.

## Pipeline Flow

### 1. TrustLayer (if CONDUCTOR_TRUSTLAYER=true)

Run the spec-driven pipeline:
- `/spec-pipeline` — full pipeline: build + review + break + merge gate
- Or individual steps: `/spec-build`, `/spec-review`, `/spec-break`

### 2. ProofShot (if CONDUCTOR_WEB_ELIGIBLE=true)

After implementing UI changes, verify visually:

```bash
# Start verification session using Conductor's port and run command
proofshot start --run "$PROOFSHOT_RUN_CMD" --port $PROOFSHOT_PORT --description "what you are verifying"

# Drive the browser
proofshot exec snapshot -i                    # See interactive elements
proofshot exec click @e3                      # Click elements
proofshot exec fill @e2 "test@example.com"    # Fill forms
proofshot exec screenshot step-name.png       # Capture proof

# Stop and bundle artifacts
proofshot stop

# Optionally post to PR
proofshot pr
```

### 3. Tests Only (if neither is available)

Run the project's test command directly based on the detected framework:
- `bun test` / `npm test` / `pnpm test` (JS/TS)
- `go test ./...` (Go)
- `pytest` (Python)
- `cargo test` (Rust)

## When NOT to Use ProofShot

- `CONDUCTOR_WEB_ELIGIBLE` is `false`
- UI type is `mobile`, `cli`, `library`, or `none`
- The change is purely backend (no UI impact)
- The project is a mobile app (iOS, Android, React Native, Flutter)

## When NOT to Use TrustLayer

- `CONDUCTOR_TRUSTLAYER` is not set or `false`
- No specs exist yet (suggest `/spec-setup` first)

## Full Pipeline Example

```bash
# 1. Check what's available
echo "$CONDUCTOR_FRAMEWORK / $CONDUCTOR_WEB_ELIGIBLE / $CONDUCTOR_TRUSTLAYER"

# 2. If TrustLayer: run spec pipeline
# /spec-pipeline

# 3. If ProofShot: visual verification
proofshot start --run "$PROOFSHOT_RUN_CMD" --port $PROOFSHOT_PORT --description "Login flow"
proofshot exec open http://localhost:$PROOFSHOT_PORT/login
proofshot exec snapshot -i
proofshot exec fill @e1 "user@test.com"
proofshot exec fill @e2 "password123"
proofshot exec screenshot step-before-submit.png
proofshot exec click @e3
proofshot exec screenshot step-after-submit.png
proofshot stop

# 4. Post proof to PR
proofshot pr
```
