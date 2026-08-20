# Working Memory - Game of Life Project

## Current Status (2026-08-20)

### Where things stand
- Project revived after the lab move. K3s, private registry and the gameoflife DNS/tunnel
  no longer exist. Everything now runs locally via `docker-compose.yml` (see CLAUDE.md).
- Stack verified: 3→9→4 engine scaling, auto-healing of removed engines, WebSocket streaming,
  controller-restart recovery.
- Barrier fast path (step when all engines ready OR 1s timeout) gives ~9 gen/s with 3 engines,
  vs the old fixed 1 gen/s.

### Changes this session (uncommitted)
- Fixed compile error in controller randomize-all (`pos` → `position`).
- Engine: hostname/own-IP fallbacks for node ID + endpoint; step-signal watchdog (10s) that
  triggers re-registration; gameLoop now actually calls register() when unregistered.
- Added docker-compose.yml, .dockerignore, Makefile compose targets; Dockerfiles on Go 1.22.

### Cleanup candidates (not done yet)
- `cmd/router/`, `Dockerfile.router`, `manifests/router-deployment.yaml` — router was removed.
- `Dockerfile.*.optimized` ×4, loose `./controller` + `./engine` binaries, `bin/`.
- `SIMPLIFIED_ARCHITECTURE.md` describes a no-barrier-sync design that contradicts the code.
- `HANDOFF_SESSION.md` is from July 2025.
- Most Go files are not gofmt'd.
- `regionId` defaults to "k3s-cluster" (REGION_ID env) — cosmetic.

### Next ideas
- Public engine needs a reachable controller again (tunnel) before it's useful.
- Layout is hardcoded 10x10 positions; >100 engines would need topology work.
