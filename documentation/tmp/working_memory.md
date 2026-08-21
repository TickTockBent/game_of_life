# Working memory — 2026-08-21 (end of day)

## Status
All five roadmap phases (A–E) shipped and live. v1.0.0 tagged; CI built amd64+arm64 images to GHCR.

## BLOCKER for "anyone can join" — one click in the GitHub UI
GHCR packages inherited the repo's private visibility, so `docker pull ghcr.io/ticktockbent/gameoflife-engine`
fails anonymously. Make the package public (no API for this):
  https://github.com/users/TickTockBent/packages/container/gameoflife-engine/settings → Danger zone → Change visibility → Public
(controller/web packages can stay private; only the engine needs to be public.)
Then verify from any machine: `docker run --rm ghcr.io/ticktockbent/gameoflife-engine` → appears on the page.

## Verified today
- Phase C: controller restart → 10 engines back in 2s; slot held on engine drop; click/reseed via WS
- Phase D: standalone container via public wss:// joined in ~1s; 3rd engine per IP refused; 35s drop survived
- Phase E: CI run 32506521751 success (engine/controller/web × amd64/arm64)

## Small follow-ups
- gen/s sparkline (roadmap B) still a number
- Phase F items: Prometheus metrics on admin port, per-engine latency, optional join token
- Rename tunnel `gameservers` → `gameoflife` in Cloudflare dashboard (cosmetic)
- Cloudflare edge caches .js/.css 4h: bump ?v= on change (or set Browser Cache TTL to respect headers)
