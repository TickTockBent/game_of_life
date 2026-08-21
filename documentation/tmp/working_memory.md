# Working memory — 2026-08-21 (evening)

## Status
- Phases A, B, C shipped and live. 10 engines on WebSocket transport, Plotter UI at /.
- Branch `ws-transport` merged to main.

## Verified today (Phase C)
- controller restart → 10/10 engines back in 2s
- engine container stop/start → slot held (`connected:false`), rejoins same slot
- lag: paused engine flagged, excluded from barrier, rejoins
- click + reseed-all go web → controller → engine socket
- `go test ./...` green (engine WS session test, grid corner-halo test, SeedPattern bounds test)

## Next: Phase D (public API surface)
- Add ingress `gameoflife-api.wshoffner.dev → http://localhost:8082` to /etc/cloudflared/gameoflife.yml
  (same tunnel f0539d6a…), `cloudflared tunnel route dns f0539d6a-a267-4786-99c2-241be68f2648 gameoflife-api.wshoffner.dev`,
  restart cloudflared-gameoflife.service. Engine default CONTROLLER_URL already points there.
- Controller limits still to add: per-IP connection cap (≤2), reserved house slots (10), per-step
  state-message rate cap. Already done: 8 KB cap, grid/engineId/displayName validation, version handshake.
- Then Phase E: GHCR multi-arch images + Join panel.

## Known
- /metrics stays public (web proxies it). Cloudflare edge caches .js/.css 4h → bump ?v= on change.
