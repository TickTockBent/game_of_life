# Distributed Conway's Game of Life

Conway's Game of Life split across many independent engine processes, kept in lockstep by a
central controller, with a real-time web view showing which engine owns which part of the grid.

Live: **https://gameoflife.wshoffner.dev**

## How it works

- **Engine** — owns one 7×7 section of the grid. Registers with the controller, gets assigned a
  slot in a 10×10 layout (so up to 100 engines / a 70×70 field), and then only acts when told to
  step. Before each step it fetches a one-cell *halo* of its neighbours' border cells from the
  controller, so patterns cross section boundaries as if it were one grid. Engines never talk to
  each other.
- **Controller** — single coordinator, channel-based (no locks). Aggregates state, serves halos,
  and runs **barrier synchronization**: it broadcasts `POST /step` to every engine once all have
  reported the previous generation, or after a 1s timeout. Engines that miss 3 steps are dropped;
  engines that hear nothing for 10s re-register. Streams state to the web tier over WebSocket.
- **Web** — serves the canvas UI and proxies `/api/*` and `/ws` to the controller. The controller
  itself is never exposed publicly.

## Running it

```bash
make up ENGINES=3      # build + start controller, web, 3 engines
make scale ENGINES=9   # add/remove engines live — the grid grows/shrinks
make status            # controller health + containers
make logs              # controller log (watch the barrier sync)
make down
```

Web UI on http://localhost:8090, controller API on http://localhost:8082. Override host ports with
`WEB_HOST_PORT` / `CONTROLLER_HOST_PORT`.

Requirements: Docker with Compose v2. Go 1.22 only if you want to build or test outside Docker
(`go build ./... && go test ./...`).

## Layout

```
cmd/controller/   coordinator (barrier sync, halo service, WebSocket fan-out)
cmd/engine/       grid-section worker
cmd/web/          static UI + reverse proxy to controller
cmd/public-engine/ standalone engine for joining a remote controller (see README.public-engine.md)
pkg/gameoflife/   core Life rules and 7x7 grid with halo support
web/public/       canvas frontend
manifests/        historical Kubernetes manifests (see below)
```

## History

This originally ran as a K3s DaemonSet across several physical nodes (ARM and x86), with a
private registry and a Cloudflare tunnel — the web copy about "a different pod on a different
physical node" dates from then. That cluster was retired in 2026; the `manifests/` directory and
the registry-based Makefile targets are kept for reference but are not maintained. Nothing in the
code assumes engines share a machine, so it could go back to a multi-host deployment by pointing
`CONTROLLER_URL` at a reachable controller.
