# netpulse

A small, single-binary internet quality monitor for your Mac. Every second it measures latency, jitter, packet loss, router (Wi‑Fi/LAN) latency, DNS time and how much of your link you are using, and it keeps a polite running estimate of your download/upload capacity. Everything is shown in a local web UI and exposed as a JSON API that a menu bar app can consume.

```
make run            # or: go run ./cmd/netpulse
open http://127.0.0.1:7171
```

No root needed: macOS allows unprivileged ICMP, so `ping`-style probes just work. Requires Go 1.22+.

## What it measures, every second

| Metric | How | Why it matters |
|---|---|---|
| **Latency** | ICMP echo to 3 independent targets (1.1.1.1, 8.8.8.8, 9.9.9.9), median | Responsiveness of everything interactive |
| **Jitter** | mean \|ΔRTT\| per target vs the previous second | Calls/video stutter on jitter more than on latency |
| **Packet loss** | lost probes / sent probes | Retransmits, call dropouts |
| **Router latency** | ICMP to your default gateway | Router slow too → Wi‑Fi/LAN problem. Only internet slow → ISP or beyond |
| **DNS** | one A lookup through your resolver (bypassing the macOS cache) | Slow "first load" of sites |
| **Your traffic** | interface byte counters (`netstat`), minus netpulse's own probe traffic | Context for everything else; free capacity lower bound |
| **Quality score** | E‑model R‑factor (0–100) from latency + jitter + loss, plus MOS | One number for "would a call feel fine right now" |
| **Status** | `good` / `degraded` / `down` per second | Availability = share of seconds not down |

Each window (1m, 5m, 15m, 1h, 6h, 24h) also reports availability, % good seconds, number of outages, longest outage, p50/p95/p99 latency, spikes and average score.

## The speed estimate (without making your network feel worse)

Measuring throughput means *using* throughput, so netpulse is deliberately polite:

1. **Only when you're idle** — your own traffic must stay under `idle_traffic_threshold_kbps` for `idle_required_for`. If nobody is using the link, nobody can feel a probe.
2. **Only when healthy** — no recent loss, latency within a few ms of baseline.
3. **Short** — each probe is sized from the last estimate to last ~300 ms.
4. **Self-aborting** — while it runs, latency is sampled every 100 ms; if it rises `abort_when_latency_rises_ms` above baseline (queues filling = you'd feel it) the transfer is cancelled immediately and probing backs off.
5. **Rare and adaptive** — starts every 2 min, grows ×1.5 each time the result is unchanged up to 30 min, resets on a network change.
6. **Budgeted** — at most `daily_data_budget_mb` per day.

Between probes, your own busiest second (last 10 min) counts as a free lower bound. The latency measured during a probe gives a **bufferbloat** figure (extra latency under load) with an A+…F grade. "Run full test" in the UI is the heavy, explicit version (4 parallel streams, ~4 s per direction).

Known limitation: the polite probes use a single connection for a fraction of a second, which can under-read very fast links (≈1 Gbps+). The full test, or a big download of your own, corrects the estimate.

## Configuration

Every setting lives in one YAML file with descriptive names and a description for each. Keys you leave out keep their defaults; unknown keys are rejected so typos are caught.

```
make config                          # writes ~/.config/netpulse/config.yaml with all defaults + docs
netpulse -print-default-config       # print the documented defaults
netpulse -config path/to/file.yaml   # use a specific file
netpulse -check-config               # validate and exit
```

Lookup order: `-config`, then `~/.config/netpulse/config.yaml`, then built-in defaults. The full reference is [`netpulse.example.yaml`](netpulse.example.yaml) (generated from `internal/config/default.yaml`, which is the single source of truth). Sections:

| Section | Controls |
|---|---|
| `web_server` | listen address |
| `latency_probes` | targets, timeout, ICMP vs TCP, router probe, route re-detection |
| `dns_probe` | on/off, hostname |
| `quality_rules` | what counts as degraded, spike thresholds, baseline window |
| `history` | in-memory retention, optional JSON-lines recording to disk |
| `speed_estimator` | every politeness knob above, endpoints, budget, full test |
| `web_ui` | default time range |

The effective config is served at `GET /api/config`.

## Run it in the background on your Mac

```
make agent-install     # builds, installs a LaunchAgent, starts now and at every login
make agent-logs
make agent-uninstall
```

## API

| Endpoint | Returns |
|---|---|
| `GET /api/summary` | latest sample, all windows, speed state, route info, `stable_for_s` — **the menu bar contract** |
| `GET /api/stream` | Server-Sent Events, one `tick` per second: `{sample, summary}` |
| `GET /api/samples?seconds=300` / `?since=<unix ms>` | per-second samples |
| `GET /api/speed` | speed estimator state and recent probes |
| `POST /api/speedtest` | start a full test (202) |
| `GET /api/config` | effective configuration and its source |

## Menu bar

**Today:** `contrib/swiftbar/netpulse.1s.sh` is a [SwiftBar](https://swiftbar.app) plugin (copy it to your SwiftBar plugin folder). It shows a colored status dot + current latency, with a dropdown of score, loss, availability, capacity and a link to the dashboard.

**Next:** a native SwiftUI `MenuBarExtra` app that reads `/api/summary` (or the SSE stream) and shows a live sparkline, with notifications for outages. Because the Go server already does all the measuring, the app stays a thin client; later the Go binary can be bundled inside the `.app` and launched as a helper.

## Layout

```
cmd/netpulse/          main: flags, config loading, wiring
internal/config/       Config struct, default.yaml (defaults + docs), validation
internal/probe/        ICMP (unprivileged) / TCP pingers, DNS timer
internal/sysnet/       default route + interface byte counters (darwin, linux)
internal/monitor/      per-second loop, ring buffer, classification, window stats
internal/speed/        polite capacity estimator + full test
internal/server/       HTTP API, SSE, embedded web UI (web/index.html, no external deps)
contrib/swiftbar/      interim menu bar plugin
packaging/macos/       LaunchAgent template
```
