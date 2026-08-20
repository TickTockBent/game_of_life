# Session Handoff

## Where we stopped (2026-08-20, HEAD `aabc44a`)

The project was revived after the lab move. It now runs as a **docker compose stack on
motherbrain** and is publicly visible at **https://gameoflife.wshoffner.dev**. Working tree clean,
all work committed on `main`.

**Running right now:** controller + web + **10 engines**, stepping ~15 gen/s, `REGION_ID=motherbrain`.
`restart: unless-stopped`, so it survives reboots. `cloudflared-gameoflife.service` (tunnel
`gameservers`, UUID `f0539d6a…`) fronts the web container only; the controller API is not public.

## How to resume

```bash
cd ~/projects/infrastructure/game_of_life
make status                      # controller health + containers
systemctl status cloudflared-gameoflife
cat documentation/ROADMAP.md     # the agreed path forward
```

If the stack is down: `make up ENGINES=10`. If the public page is down but local works:
`sudo systemctl restart cloudflared-gameoflife`.

## What's next

`documentation/ROADMAP.md` lays out phases A–E (controller groundwork → UI rebuild → WS engine
transport → public API → GHCR + "Join" panel). **Two decisions are pending before Phase A/E:**

1. Step cadence — roadmap proposes a fixed ~4 gen/s (`STEP_INTERVAL`); faster looks livelier,
   slower is kinder to remote engines.
2. Join panel — collect display name only, or also an optional free-text location label?

Start with Phase A (spiral slot packing, fixed tick, miss budget, admin-port split); it improves
the public page immediately without touching the protocol.

## Loose ends

- **Cleanup not done:** `cmd/router/`, `Dockerfile.router`, `manifests/router-deployment.yaml`,
  four `Dockerfile.*.optimized`, loose `./controller` + `./engine` binaries and `bin/`,
  `SIMPLIFIED_ARCHITECTURE.md` (describes a no-barrier design that contradicts the code),
  `README.public-engine.md` (describes the NAT-broken push model). Most Go files are not gofmt'd.
  Roadmap Phase C deletes most of this.
- **`cmd/public-engine` is not usable by outsiders** — the controller pushes `/step` to engines,
  so participants must be inbound-reachable; its IP auto-detection returns the LAN address.
  Roadmap D1–D3 replaces it. Don't advertise the one-liner until Phase E.
- **Tunnel is still named `gameservers`** in Cloudflare; rename to `gameoflife` in the dashboard
  (no CLI rename). Notebook carries the TODO.
- **10 engines render as a 70×7 strip** (linear slot assignment) — Phase A fixes.
- `manifests/` and registry Makefile targets are historical; kept for reference only.

## Lab notebook

`~/homelab/external-access.md` and `services.md` were updated this session (tunnel table row,
compose project entry, corrected the previously-wrong "orphaned credential" story). They match
reality as of this commit.
