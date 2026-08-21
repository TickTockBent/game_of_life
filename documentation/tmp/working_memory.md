# Working memory — 2026-08-21

## Status
- Compose stack on motherbrain, 10 engines, public at https://gameoflife.wshoffner.dev.
- **Phase A of ROADMAP.md shipped** (controller groundwork). Verified live:
  - spiral slot packing → 10 engines form a blob around (4,4)/(4,5)
  - fixed tick 250ms → 3.9 gen/s measured
  - lagging engine excluded from barrier → 3.2 gen/s with one engine paused (was 1 gen/s)
  - `/debug/*` 404 on :8082, works on container-internal :8091
  - eject after 40 misses (~10s), engine re-registers ~3s after recovery

## Next: Phase B (UI rebuild, web/public/)
- `/topology` and `/aggregated-state` now carry `lagging: true` per node — draw those sections dimmed.
- Empty slots: dotted outlines. No text on canvas. Owner hue from stable hash of podId.
- Light/dark via CSS custom properties + toggle.

## Known/pre-existing
- `barrierCoordinator` and `broadcastStepToAllEngines` read `c.nodes` off the message-processor
  goroutine (data race in principle, benign in practice). Goes away in Phase C's restructure.
- Public page's "randomize all" button goes through the web tier's own fan-out to engines, so it
  still works for anonymous visitors. Decide in Phase D whether that's wanted.
