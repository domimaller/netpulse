import AppKit
import Foundation
import ServiceManagement

/// Polls the netpulse server once a second and holds everything the menu bar
/// icon and the panel display.
@MainActor
final class Store: ObservableObject {
    static let shared = Store()

    @Published private(set) var summary: Summary?
    @Published private(set) var samples: [Sample] = [] // newest last, trimmed to the chart window
    @Published private(set) var reachable = false
    @Published private(set) var menuConfig = MenuBarConfig.defaults
    @Published private(set) var configSource = ""
    @Published private(set) var message: String?
    @Published private(set) var launchAtLogin = SMAppService.mainApp.status == .enabled
    /// Latency text next to the icon. The panel toggle is remembered per user;
    /// until it's used, menu_bar.show_latency_next_to_icon from the config decides.
    @Published private(set) var showLatency = MenuBarConfig.defaults.showLatencyNextToIcon
    private static let showLatencyKey = "showLatencyNextToIcon"
    @Published private(set) var launchAtLoginError: String?

    /// Override with: defaults write io.github.domimaller.netpulse.bar serverURL http://127.0.0.1:7171
    let baseURL: URL
    let server = ServerProcess()

    private var started: Int64?
    private var failures = 0
    private var loop: Task<Void, Never>?
    private let session: URLSession
    private let decoder: JSONDecoder

    private init() {
        let s = UserDefaults.standard.string(forKey: "serverURL") ?? "http://127.0.0.1:7171"
        baseURL = URL(string: s) ?? URL(string: "http://127.0.0.1:7171")!
        let cfg = URLSessionConfiguration.ephemeral
        cfg.timeoutIntervalForRequest = 2
        cfg.requestCachePolicy = .reloadIgnoringLocalCacheData
        session = URLSession(configuration: cfg)
        decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        if let saved = UserDefaults.standard.object(forKey: Self.showLatencyKey) as? Bool {
            showLatency = saved
        }
        start()
    }

    // MARK: - Derived state

    /// Quality score averaged over `icon_score_smoothing_window` (down seconds count as 0).
    var smoothedScore: Double? {
        let n = menuConfig.smoothingSeconds
        let recent = samples.suffix(n)
        guard !recent.isEmpty else { return summary?.now?.score }
        return recent.map { $0.isDown ? 0 : $0.score }.reduce(0, +) / Double(recent.count)
    }

    var level: Level {
        guard reachable, let now = summary?.now else { return .offline }
        if now.isDown { return .down }
        return Level.from(score: smoothedScore ?? now.score, menuConfig)
    }

    var dashboardURL: URL { baseURL }

    // MARK: - Polling

    private func start() {
        guard loop == nil else { return }
        loop = Task { [weak self] in
            await self?.ensureServer()
            while !Task.isCancelled {
                await self?.tick()
                try? await Task.sleep(nanoseconds: 1_000_000_000)
            }
        }
    }

    /// Use a running server if there is one; otherwise start the bundled one.
    private func ensureServer() async {
        if await healthy() { return }
        if let err = server.launch() {
            message = err
            return
        }
        for _ in 0..<20 { // wait up to ~5s for it to come up
            if await healthy() { return }
            try? await Task.sleep(nanoseconds: 250_000_000)
        }
    }

    private func tick() async {
        do {
            let s: Summary = try await get("/api/summary")
            if s.info.started != started { // first contact, or the server restarted
                started = s.info.started
                await reloadConfigAndHistory()
            }
            if let now = s.now, samples.last?.t != now.t {
                samples.append(now)
                trim()
            }
            summary = s
            reachable = true
            failures = 0
            message = nil
        } catch {
            reachable = false
            failures += 1
            // Server gone (crashed, agent stopped, port freed): try to bring ours back.
            if failures % 5 == 0, !server.isRunning {
                if let err = server.launch() { message = err }
            }
        }
    }

    private func reloadConfigAndHistory() async {
        if let c: ConfigResponse = try? await get("/api/config") {
            if let mb = c.config.menuBar {
                menuConfig = mb
                if UserDefaults.standard.object(forKey: Self.showLatencyKey) == nil {
                    showLatency = mb.showLatencyNextToIcon
                }
            }
            configSource = c.source
        }
        if let h: [Sample] = try? await get("/api/samples?seconds=\(menuConfig.chartSeconds)") {
            samples = h
            trim()
        }
    }

    private func trim() {
        let keep = max(menuConfig.chartSeconds, menuConfig.smoothingSeconds)
        if samples.count > keep { samples.removeFirst(samples.count - keep) }
    }

    private func healthy() async -> Bool {
        guard let url = URL(string: baseURL.absoluteString + "/healthz") else { return false }
        guard let result = try? await session.data(from: url) else { return false }
        return (result.1 as? HTTPURLResponse)?.statusCode == 200
    }

    private func get<T: Decodable>(_ path: String) async throws -> T {
        guard let url = URL(string: baseURL.absoluteString + path) else { throw URLError(.badURL) }
        let (data, resp) = try await session.data(from: url)
        guard (resp as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
        return try decoder.decode(T.self, from: data)
    }

    // MARK: - Actions

    func runSpeedTest() {
        guard let url = URL(string: baseURL.absoluteString + "/api/speedtest") else { return }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        Task { _ = try? await session.data(for: req) }
    }

    func openDashboard() {
        NSWorkspace.shared.open(dashboardURL)
    }

    func startServerNow() {
        if let err = server.launch() { message = err }
    }

    func setShowLatency(_ on: Bool) {
        showLatency = on
        UserDefaults.standard.set(on, forKey: Self.showLatencyKey)
    }

    func setLaunchAtLogin(_ on: Bool) {
        do {
            if on { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            launchAtLoginError = nil
        } catch {
            launchAtLoginError = "Couldn't change login item: \(error.localizedDescription)"
        }
        launchAtLogin = SMAppService.mainApp.status == .enabled
    }

    func shutdown() {
        loop?.cancel()
        server.stop()
    }
}
