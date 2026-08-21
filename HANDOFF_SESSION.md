# Session Handoff

## Where we stopped (2026-08-21, HEAD `339553b`, tag `v1.0.0`, pushed to origin/main)

All five roadmap phases are shipped and running:

- **A** controller groundwork: spiral slots, fixed 4 gen/s tick, soft-skip laggards, admin port 8091
- **B** UI: "Plotter" design at `/` (graph paper drawn on the cell lattice, color-by-engine OFF by
  default with a toggle, light/dark), `/stats.html` nerd stats, `/classic/` old UI, `/themes/` candidates
- **C** engines dial the controller over ONE WebSocket (`/engine`); halo rides in the step message
  (corners fixed); pattern-based reseed; HTTP push, router, public-engine, pkg/grid deleted
- **D** `gameoflife-api.wshoffner.dev` → controller public port via the existing tunnel; per-IP cap,
  reserved house slots, rate cap, IPs hidden, two-tier eject budget
- **E** GHCR multi-arch images via GitHub Actions; Join panel on the page; README leads with joining

**Running right now:** controller + web + 10 engines on motherbrain (compose, `restart: unless-stopped`),
public at https://gameoflife.wshoffner.dev, engine API at https://gameoflife-api.wshoffner.dev.

## Public joining: verified end-to-end (2026-08-21)

`ghcr.io/ticktockbent/gameoflife-engine` was flipped to Public in the GitHub UI (packages inherit the
private repo's visibility; controller/web images stay private). Wes ran the Join-panel command from
an outside machine and `test1` appeared on the page. Nothing blocks "anyone can join" any more.

## How to resume

```bash
cd ~/projects/infrastructure/game_of_life
make status                              # controller health + containers
systemctl status cloudflared-gameoflife  # tunnel (both hostnames)
cat documentation/tmp/working_memory.md  # latest findings
gh run list --limit 3                    # image builds
```

Stack down → `make up ENGINES=10`. Public page down but local fine → `sudo systemctl restart cloudflared-gameoflife`.
Pause/step/reseed-all: `docker compose exec controller wget -qO- --post-data= http://localhost:8091/debug/pause` (admin port, container-only).
**After any JS/CSS change bump the `?v=` in the HTML** — Cloudflare edge-caches by extension for 4 h.

## Wes's testing notes (what to look for)

- Join from outside the LAN (phone hotspot): should appear within seconds, hover shows the name,
  `/stats.html` lists it as `public`. A third engine from the same address is refused.
- Kill the laptop's Wi-Fi for 30 s: section dims (lagging), slot is held, catches up on return.
- If something misbehaves: `docker compose logs -f controller` — every register/refuse/lag/eject is logged.

## Loose ends / next

- **Phase F (hardening)**: Prometheus metrics on the admin port (scrape from the lab stack), per-engine
  latency in the participants list, optional join token if abuse shows up.
- gen/s sparkline is still just a number.
- `/metrics` is public (web proxies it); harmless but revisit.
- Rename tunnel `gameservers` → `gameoflife` in the Cloudflare dashboard (cosmetic).
- Optionally set the zone's Browser Cache TTL to "Respect existing headers" and drop the `?v=` dance.
- `scripts/` and `manifests/` are still the historical K3s set — untouched, could be pruned.

## Lab notebook

`~/homelab/external-access.md` (both hostnames, tunnel row, edge-cache gotcha) and `services.md`
(compose entry) were updated this session and match reality.
