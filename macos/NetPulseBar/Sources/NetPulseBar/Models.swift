import Foundation

// Mirrors the JSON served by the Go server (snake_case keys are converted
// automatically). Everything optional that the server may send as null.

struct Sample: Decodable, Identifiable, Equatable {
    let t: Int64
    let rttMs: Double?
    let jitterMs: Double?
    let lossPct: Double
    let gatewayMs: Double?
    let dnsMs: Double?
    let rxBps: Double
    let txBps: Double
    let score: Double
    let mos: Double
    let status: String
    let speedProbe: Bool?

    var id: Int64 { t }
    var date: Date { Date(timeIntervalSince1970: Double(t) / 1000) }
    var isDown: Bool { status == "down" }
}

struct Window: Decodable {
    let label: String
    let seconds: Int
    let samples: Int
    let availabilityPct: Double
    let goodPct: Double
    let downSeconds: Int
    let degradedSeconds: Int
    let outages: Int
    let longestOutageS: Int
    let spikes: Int
    let rttMinMs: Double?
    let rttP50Ms: Double?
    let rttP95Ms: Double?
    let jitterMs: Double?
    let lossPct: Double
    let gatewayP50Ms: Double?
    let dnsP50Ms: Double?
    let scoreAvg: Double
    let mosAvg: Double
}

struct SpeedState: Decodable {
    let enabled: Bool
    let downBps: Double?
    let upBps: Double?
    let passivePeakRxBps: Double
    let passivePeakTxBps: Double
    let capacityDownBps: Double?
    let capacityUpBps: Double?
    let bufferbloatMs: Double?
    let status: String
    let probing: Bool
    let fullRunning: Bool
}

struct Route: Decodable {
    let gateway: String
    let interface: String
}

struct ServerInfo: Decodable {
    let mode: String
    let targets: [String]
    let route: Route
    let dnsName: String
    let started: Int64
}

struct Summary: Decodable {
    let now: Sample?
    let windows: [Window]?
    let speed: SpeedState
    let info: ServerInfo
    let stableForS: Int64

    func window(_ label: String) -> Window? { windows?.first { $0.label == label } }
    /// The longest window up to `label` that exists (short uptime = fewer windows).
    func window(atMost labels: [String]) -> Window? {
        for l in labels { if let w = window(l) { return w } }
        return nil
    }
}

/// The `menu_bar` section of the netpulse config (served by /api/config).
struct MenuBarConfig: Decodable, Equatable {
    var excellentScoreFrom: Double
    var goodScoreFrom: Double
    var fairScoreFrom: Double
    var poorScoreFrom: Double
    var iconScoreSmoothingWindow: Int64 // nanoseconds
    var showLatencyNextToIcon: Bool
    var panelChartWindow: Int64 // nanoseconds

    var smoothingSeconds: Int { max(1, Int(iconScoreSmoothingWindow / 1_000_000_000)) }
    var chartSeconds: Int { max(30, Int(panelChartWindow / 1_000_000_000)) }

    static let defaults = MenuBarConfig(
        excellentScoreFrom: 85, goodScoreFrom: 75, fairScoreFrom: 65, poorScoreFrom: 50,
        iconScoreSmoothingWindow: 10_000_000_000, showLatencyNextToIcon: true,
        panelChartWindow: 120_000_000_000)
}

struct ConfigResponse: Decodable {
    struct Inner: Decodable { let menuBar: MenuBarConfig? }
    let source: String
    let config: Inner
}

/// Quality level that drives the icon colour.
enum Level: Equatable {
    case excellent, good, fair, poor, bad, down, offline

    var title: String {
        switch self {
        case .excellent: return "Excellent"
        case .good: return "Good"
        case .fair: return "Fair"
        case .poor: return "Poor"
        case .bad: return "Bad"
        case .down: return "Down"
        case .offline: return "netpulse not running"
        }
    }

    static func from(score: Double, _ c: MenuBarConfig) -> Level {
        if score >= c.excellentScoreFrom { return .excellent }
        if score >= c.goodScoreFrom { return .good }
        if score >= c.fairScoreFrom { return .fair }
        if score >= c.poorScoreFrom { return .poor }
        return .bad
    }
}

// MARK: - Formatting

enum Fmt {
    static func ms(_ v: Double?) -> String {
        guard let v else { return "–" }
        return v < 10 ? String(format: "%.1f", v) : String(Int(v.rounded()))
    }

    static func mbps(_ bps: Double?) -> String {
        guard let bps, bps > 0 else { return "–" }
        let m = bps / 1e6
        if m >= 1000 { return String(format: "%.2f Gbps", m / 1000) }
        if m >= 100 { return "\(Int(m.rounded())) Mbps" }
        if m >= 1 { return String(format: "%.1f Mbps", m) }
        return "\(Int((bps / 1e3).rounded())) kbps"
    }

    static func pct(_ v: Double, digits: Int = 1) -> String {
        if v >= 99.995 { return "100%" }
        return String(format: "%.\(digits)f%%", v)
    }

    static func duration(_ s: Int64) -> String {
        let s = max(0, s)
        if s < 60 { return "\(s)s" }
        if s < 3600 { return "\(s / 60)m \(s % 60)s" }
        return "\(s / 3600)h \((s % 3600) / 60)m"
    }
}
