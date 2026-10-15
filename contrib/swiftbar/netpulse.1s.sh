#!/bin/bash
# <swiftbar.title>netpulse</swiftbar.title>
# <swiftbar.hideRunInTerminal>true</swiftbar.hideRunInTerminal>
# <swiftbar.hideLastUpdated>true</swiftbar.hideLastUpdated>
#
# Interim menu bar for netpulse until the native app exists.
# Works with SwiftBar (https://swiftbar.app) or xbar. Needs jq (bundled with macOS 15+).
# The "1s" in the file name is the refresh interval.

URL="${NETPULSE_URL:-http://127.0.0.1:7171}"
J=$(curl -fsS --max-time 0.8 "$URL/api/summary") || { echo "◌ off | color=gray"; echo "---"; echo "netpulse is not running"; exit 0; }

jq -r --arg url "$URL" '
  def ms: if . == null then "–" elif . < 10 then (. * 10 | round / 10 | tostring) else (round | tostring) end;
  def mbps: if . == null then "–" else (. / 1e6 | if . >= 100 then round else (. * 10 | round / 10) end | tostring) + " Mbps" end;
  (.windows | map({(.label): .}) | add) as $w
  | .now as $n
  | (if $n.status == "down" then "✕" elif $n.status == "degraded" then "!" else "●" end) as $ic
  | (if $n.status == "down" then "red" elif $n.status == "degraded" then "orange" else "green" end) as $col
  | "\($ic) \($n.rtt_ms | ms) ms | color=\($col) sfcolor=\($col)",
    "---",
    "Status: \($n.status)   score \($n.score | round)/100",
    "Latency \($n.rtt_ms | ms) ms · jitter \($n.jitter_ms | ms) ms · router \($n.gateway_ms | ms) ms",
    "Loss 5m: \($w["5m"].loss_pct // $w["1m"].loss_pct) %",
    "Availability 1h: \(($w["1h"] // $w["15m"] // $w["5m"] // $w["1m"]).availability_pct) %",
    "Capacity ↓ \(.speed.capacity_down_bps | mbps)  ↑ \(.speed.capacity_up_bps | mbps)",
    "Stable for \(.stable_for_s) s",
    "---",
    "Open dashboard | href=\($url)"
' <<<"$J"
