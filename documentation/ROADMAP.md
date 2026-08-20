# Roadmap: slicker UI + one-command public participation

*Written 2026-08-20. Baseline: commit f9c0a52+, compose stack on motherbrain, 10 engines,
public read-only view at https://gameoflife.wshoffner.dev.*

## Goals

1. **UI** — a grid that looks like a deliberate piece of design: no IDs stamped over the cells,
   light/dark, responsive, participants shown as people rather than pod hashes.
2. **Participation** — `docker run ticktockbent/gameoflife-engine` from any laptop/Pi/NAS, behind
   NAT, and your section appears on the public page within seconds. Nothing to configure.

## Key design decisions (decide once, up front)

| # | Decision | Why |
|---|---|---|
| D1 | **Engines initiate a WebSocket to the controller; the controller never connects to engines.** | The only NAT/CGNAT-proof shape. Kills `EXTERNAL_IP`, port-forwarding, and the failed `detectExternalIP()`. Works through the existing Cloudflare tunnel. |
| D2 | **One engine binary, one transport.** Local compose engines use the same WS path as public ones; `cmd/public-engine` and the HTTP push (`POST /step`, `/randomize`) go away. | Two code paths rotted last time. Compose becomes a test of exactly what strangers run. |
| D3 | **Step message carries the halo.** Controller → engine: `{step, generation, halo[9][9]}`. Engine → controller: `{state, generation, grid[7][7]}`. | Drops the per-step `GET /generation` + `GET /halo` round trips (3 RTTs → 1). Matters a lot once engines are 100 ms away. |
| D4 | **Fixed tick, not as-fast-as-possible.** Controller targets e.g. 4 gen/s (`STEP_INTERVAL`), still barrier-gated with a timeout. | Today's "step the instant all 10 are ready" = 15 gen/s locally and would become "step at the speed of the slowest stranger". A steady cadence is also nicer to watch. |
| D5 | **Laggards are soft-skipped, not ejected.** Miss budget per engine; a skipped engine's section freezes (drawn dimmed) and rejoins on its next good step. Eject only after prolonged silence. | Residential links hiccup. Ejecting on 3 misses would churn the grid. |
| D6 | **Compact slot packing.** New engines get the free slot nearest the centre of the 10×10 layout (spiral), not the lowest index. | 10 engines today render as a 70×7 strip. Spiral packing gives a blob that grows outward as people join — the visual payoff of participation. |
| D7 | **Public surface ≠ admin surface.** Controller listens on two ports: public (`/engine` WS, `/ws`, read APIs, `/api/click`) and admin (`/debug/*`, `/metrics`, `/api/randomize`). Only public goes through the tunnel. | Cheaper and safer than per-route auth. |
| D8 | **No join token in v1.** Abuse controls are structural: slot cap, per-IP connection cap, payload validation, message-size/rate limits, reserved slots for house engines. | "One docker command" means no signup. Add an optional token later if abuse appears. |
| D9 | **Images on GHCR, multi-arch (amd64 + arm64).** Built by GitHub Actions on tag. | Pi/Mac users are the likely joiners. No private registry to resurrect. |

## Phases

Each phase is independently shippable and leaves the public page working.

### Phase A — Controller groundwork (small, high leverage)
- Spiral slot assignment (D6). `processRegister` only.
- Fixed tick + miss budget + dimmed-state flag on `/aggregated-state` (D4, D5).
- Split admin listener (D7). Compose publishes only the public port.
- **Exit:** 10 local engines form a compact blob, step at a steady 4 gen/s, `/debug/*` unreachable on 8082.

### Phase B — UI rebuild (`web/public/`, still vanilla JS + canvas, no build step)
- Layout: grid is the hero, full-bleed, canvas scales to viewport (devicePixelRatio-aware).
- Cells tinted per owner (stable hash of engine ID → hue; light/dark variants); section borders as
  faint hairlines; **no text on the canvas**. Hover/tap a section → floating card with owner name,
  generation, join time, location-ish (engine-reported `hostname`/`label` only; nothing inferred).
- Light/dark via CSS custom properties, `prefers-color-scheme` + toggle persisted in localStorage.
- Side panel: live participants list (display names, coloured swatches, "joined 3 m ago"),
  generation counter, gen/s sparkline, "X engines · Y cells". Collapsible on mobile.
- Retire the raw queue-size tiles; keep them behind a "nerd stats" disclosure.
- Empty slots rendered as subtle dotted outlines so a growing grid reads as "room to join".
- **Exit:** looks intentional on phone + desktop in both themes; no node IDs over the grid.

### Phase C — WebSocket engine transport (D1–D3)
- Controller: `/engine` WS handler → `hello{displayName, version}` / `assigned{position}` /
  `step{gen, halo}` / `state{gen, grid}` / `ping`. Internally an `EngineTransport` interface so
  the barrier code doesn't care; HTTP push implementation deleted once compose is migrated.
- Engine: replace register/getHalo/getGeneration/pushState/HTTP server with one WS client +
  reconnect-with-backoff. Keeps its grid across reconnects. Default `CONTROLLER_URL` =
  `wss://gameoflife-api.wshoffner.dev`; compose overrides to `ws://controller:8081`.
- Delete `cmd/public-engine`, `cmd/router`, `Dockerfile.router`, `*.optimized`, loose binaries.
- **Exit:** compose stack runs on WS only; `docker compose restart controller` → engines back in < 5 s.

### Phase D — Public API surface
- Second tunnel ingress: `gameoflife-api.wshoffner.dev → localhost:8082` (same `gameservers`
  tunnel, same systemd unit). Cloudflare WS passthrough is on by default; engine pings every 30 s
  to stay under the 100 s idle cut.
- Limits: ≤ 2 engines per source IP, ≤ 90 public slots (10 reserved for house engines), 8 KB
  message cap, 1 state msg per step, strict `[7][7]bool` validation, display names sanitised
  (length, charset) — they're rendered on the public page.
- **Exit:** a laptop on phone tethering runs the engine, shows up on the page, survives a 30 s
  network drop.

### Phase E — Distribution + "Join" UX
- GitHub Actions: on `v*` tag, buildx amd64+arm64 → `ghcr.io/ticktockbent/gameoflife-engine`
  (and `-controller`, `-web` for the house stack).
- Public page gets a **Join** panel: the one-liner with a copy button, a `DISPLAY_NAME` field that
  rewrites the command, and "your engine will appear here" feedback.
  `docker run -d --name life -e DISPLAY_NAME="Wes" ghcr.io/ticktockbent/gameoflife-engine`
- README: joining section front and centre; house-stack instructions below.
- **Exit:** someone who has never seen the repo joins from the command on the page in < 1 minute.

### Phase F — Hardening (as needed)
Version handshake + "please upgrade" message, Prometheus metrics on the admin port (scrape from
the existing stack), per-engine latency shown in the participants list, optional join token.

## Order and effort

A → B → C → D → E. A and B are visible on the public page immediately and don't touch the
protocol; C is the one genuinely risky change (do it on a branch, keep compose green). Rough
sizing: A half-day, B one to two days, C one day, D half-day, E half-day.

## Out of scope (for now)
Layouts beyond 10×10 / 100 engines; multiple regions/controllers; user accounts; persistence of
grid state across controller restarts.
