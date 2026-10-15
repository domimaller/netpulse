import Charts
import SwiftUI

/// The drop-down panel shown when the menu bar icon is clicked.
struct PanelView: View {
    @EnvironmentObject var store: Store

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            header
            if store.reachable, let s = store.summary {
                tiles(s)
                chart
                speedRow(s.speed)
            } else {
                offline
            }
            Divider()
            settings
            footer
        }
        .padding(14)
        .frame(width: 380)
    }

    // MARK: Header

    private var header: some View {
        let level = store.level
        let score = store.smoothedScore.map { Int($0.rounded()) }
        return HStack(alignment: .center, spacing: 10) {
            Circle()
                .fill(Palette.color(level))
                .frame(width: 14, height: 14)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(level.title).font(.headline)
                    if let score, store.reachable {
                        Text("\(score)/100").font(.headline).monospacedDigit().foregroundStyle(.secondary)
                    }
                }
                if let s = store.summary, store.reachable {
                    Text("Stable for \(Fmt.duration(s.stableForS)) · \(s.info.route.interface) via \(s.info.route.gateway)")
                        .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            Spacer()
        }
        .help("Quality score (E-model R-factor, 0–~93) averaged over the last \(store.menuConfig.smoothingSeconds)s. It sets the icon colour: deep green ≥ \(Int(store.menuConfig.excellentScoreFrom)), green ≥ \(Int(store.menuConfig.goodScoreFrom)), yellow ≥ \(Int(store.menuConfig.fairScoreFrom)), orange ≥ \(Int(store.menuConfig.poorScoreFrom)), red below.")
    }

    // MARK: Tiles

    private func tiles(_ s: Summary) -> some View {
        let now = s.now
        let w1 = s.window("1m")
        let w5 = s.window(atMost: ["5m", "1m"])
        let wh = s.window(atMost: ["1h", "15m", "5m", "1m"])
        let sp = s.speed
        let cols = [GridItem(.flexible(), spacing: 8), GridItem(.flexible(), spacing: 8), GridItem(.flexible(), spacing: 8)]
        return LazyVGrid(columns: cols, spacing: 8) {
            Tile(title: "Latency", value: Fmt.ms(now?.rttMs), unit: "ms",
                 detail: "p95 \(Fmt.ms(w1?.rttP95Ms))",
                 info: "Round-trip time to the internet targets right now. Detail: 95th percentile over the last minute.")
            Tile(title: "Jitter", value: Fmt.ms(now?.jitterMs), unit: "ms",
                 detail: "avg \(Fmt.ms(w1?.jitterMs))",
                 info: "How much latency changes from second to second. High jitter makes calls stutter.")
            Tile(title: "Loss · 5m", value: w5.map { String(format: "%.1f", $0.lossPct) } ?? "–", unit: "%",
                 detail: "\(w5?.degradedSeconds ?? 0) degraded s",
                 info: "Share of probes that got no answer in the last 5 minutes.")
            Tile(title: "Uptime · \(wh?.label ?? "1h")", value: wh.map { Fmt.pct($0.availabilityPct, digits: 2) } ?? "–", unit: "",
                 detail: "\(wh?.outages ?? 0) outages",
                 info: "Share of seconds in which at least one target answered.")
            Tile(title: "Download", value: Fmt.mbps(sp.capacityDownBps), unit: "",
                 detail: sp.downBps == nil ? "your peak" : "probe + peak",
                 info: "Estimated download capacity: the higher of the last speed probe and your own busiest second recently.")
            Tile(title: "Upload", value: Fmt.mbps(sp.capacityUpBps), unit: "",
                 detail: sp.upBps == nil ? "your peak" : "probe + peak",
                 info: "Estimated upload capacity: the higher of the last upload probe and your own busiest upload second recently.")
            Tile(title: "Router", value: Fmt.ms(now?.gatewayMs), unit: "ms",
                 detail: "p50 \(Fmt.ms(w1?.gatewayP50Ms))",
                 info: "Ping to your router. If this is slow too, the problem is Wi-Fi/LAN, not your provider.")
            Tile(title: "DNS", value: Fmt.ms(now?.dnsMs), unit: "ms",
                 detail: "p50 \(Fmt.ms(w1?.dnsP50Ms))",
                 info: "Time for your DNS resolver to answer one lookup.")
            Tile(title: "Bufferbloat", value: sp.bufferbloatMs.map { "+" + Fmt.ms($0) } ?? "–", unit: sp.bufferbloatMs == nil ? "" : "ms",
                 detail: "under load",
                 info: "Extra latency while the link is busy, measured during download probes. High values mean calls lag whenever something downloads.")
        }
    }

    // MARK: Chart

    private var chart: some View {
        let data = Array(store.samples.suffix(store.menuConfig.chartSeconds))
        let values = data.compactMap { $0.rttMs }.sorted()
        let p95 = values.isEmpty ? 50 : values[min(values.count - 1, Int(Double(values.count) * 0.95))]
        let yMax = max(20, p95 * 1.4)
        return VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("Latency · last \(Fmt.duration(Int64(store.menuConfig.chartSeconds)))")
                    .font(.caption).foregroundStyle(.secondary)
                Spacer()
                LegendDot(color: .blue, label: "Internet")
                LegendDot(color: .orange, label: "Router")
            }
            Chart {
                ForEach(data) { s in
                    if let r = s.rttMs {
                        LineMark(x: .value("Time", s.date), y: .value("ms", min(r, yMax)),
                                 series: .value("Series", "Internet"))
                            .foregroundStyle(.blue)
                            .lineStyle(StrokeStyle(lineWidth: 1.5))
                    }
                    if let g = s.gatewayMs {
                        LineMark(x: .value("Time", s.date), y: .value("ms", min(g, yMax)),
                                 series: .value("Series", "Router"))
                            .foregroundStyle(.orange)
                            .lineStyle(StrokeStyle(lineWidth: 1.5))
                    }
                }
            }
            .chartYScale(domain: 0...yMax)
            .chartXAxis(.hidden)
            .chartYAxis {
                AxisMarks(position: .leading, values: .automatic(desiredCount: 3)) { _ in
                    AxisGridLine()
                    AxisValueLabel()
                }
            }
            .frame(height: 90)
            StatusStrip(samples: data)
                .frame(height: 10)
                .help("One cell per second: green good, yellow degraded, red down.")
        }
    }

    // MARK: Speed

    private func speedRow(_ sp: SpeedState) -> some View {
        HStack(spacing: 8) {
            Text(sp.status)
                .font(.caption).foregroundStyle(.secondary)
                .lineLimit(2)
                .help("Automatic speed probes only run when you are idle and stop instantly if latency rises.")
            Spacer()
            Button(sp.fullRunning ? "Testing…" : "Run speed test") { store.runSpeedTest() }
                .disabled(sp.fullRunning)
                .controlSize(.small)
        }
    }

    // MARK: Offline

    private var offline: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Can't reach netpulse at \(store.baseURL.absoluteString).")
                .font(.callout)
            if let m = store.message {
                Text(m).font(.caption).foregroundStyle(.secondary)
            }
            Button("Start netpulse") { store.startServerNow() }
                .controlSize(.small)
        }
    }

    // MARK: Settings

    private var settings: some View {
        HStack {
            Toggle("Show latency in menu bar", isOn: Binding(
                get: { store.showLatency },
                set: { store.setShowLatency($0) }))
                .toggleStyle(.checkbox)
                .help("Show the current latency next to the icon. When off, only the coloured dot is shown (red with an ✕ when the connection is down).")
            Spacer()
            LaunchAtLoginToggle()
        }
        .controlSize(.small)
    }

    // MARK: Footer

    private var footer: some View {
        HStack {
            Button("Open dashboard") { store.openDashboard() }
                .keyboardShortcut("d")
            Spacer()
            Button("Quit") { NSApp.terminate(nil) }
                .keyboardShortcut("q")
        }
        .controlSize(.small)
    }
}

// MARK: - Pieces

struct Tile: View {
    let title: String
    let value: String
    let unit: String
    let detail: String
    let info: String

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            HStack(alignment: .firstTextBaseline, spacing: 2) {
                Text(value).font(.system(size: 17, weight: .semibold)).monospacedDigit()
                    .lineLimit(1).minimumScaleFactor(0.7)
                if !unit.isEmpty {
                    Text(unit).font(.caption).foregroundStyle(.secondary)
                }
            }
            Text(detail).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
        }
        .padding(8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.06)))
        .help(info)
    }
}

struct LegendDot: View {
    let color: Color
    let label: String

    var body: some View {
        HStack(spacing: 4) {
            RoundedRectangle(cornerRadius: 1).fill(color).frame(width: 10, height: 2)
            Text(label).font(.caption2).foregroundStyle(.secondary)
        }
    }
}

/// One thin cell per second, coloured by that second's status.
struct StatusStrip: View {
    let samples: [Sample]

    var body: some View {
        Canvas { ctx, size in
            guard !samples.isEmpty else { return }
            let w = size.width / CGFloat(samples.count)
            for (i, s) in samples.enumerated() {
                let rect = CGRect(x: CGFloat(i) * w, y: 0, width: max(w - (w > 3 ? 1 : 0), 0.5), height: size.height)
                ctx.fill(Path(roundedRect: rect, cornerRadius: min(2, w / 3)), with: .color(Palette.status(s.status)))
            }
        }
    }
}

/// Registers the app as a login item (macOS 13+). Works best when NetPulse.app
/// lives in /Applications or ~/Applications. State lives in Store (not @State):
/// with only the Command Line Tools installed, newer SDKs can't expand the
/// SwiftUI @State macro.
struct LaunchAtLoginToggle: View {
    @EnvironmentObject var store: Store

    var body: some View {
        Toggle("Launch at login", isOn: Binding(
            get: { store.launchAtLogin },
            set: { store.setLaunchAtLogin($0) }))
            .toggleStyle(.checkbox)
            .help(store.launchAtLoginError ?? "Start NetPulse automatically when you log in.")
    }
}
