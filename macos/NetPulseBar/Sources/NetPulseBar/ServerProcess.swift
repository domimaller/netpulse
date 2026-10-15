import Foundation

/// Starts and stops the Go `netpulse` server bundled inside NetPulse.app
/// (Contents/MacOS/netpulse). If a server is already answering (e.g. you run
/// `make run` or the LaunchAgent), the app simply uses that one instead.
final class ServerProcess {
    private(set) var process: Process?

    var isRunning: Bool { process?.isRunning ?? false }

    /// Bundled helper first; NETPULSE_BIN lets `swift run` use a dev build.
    var binaryURL: URL? {
        if let u = Bundle.main.url(forAuxiliaryExecutable: "netpulse") { return u }
        if let env = ProcessInfo.processInfo.environment["NETPULSE_BIN"] {
            return URL(fileURLWithPath: env)
        }
        return nil
    }

    var logURL: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/netpulse.log")
    }

    /// Launches the server. Returns a human readable error, or nil on success.
    @discardableResult
    func launch() -> String? {
        if isRunning { return nil }
        guard let bin = binaryURL else {
            return "netpulse binary not found inside the app (build it with `make app`)"
        }
        let p = Process()
        p.executableURL = bin
        let fm = FileManager.default
        try? fm.createDirectory(at: logURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        if !fm.fileExists(atPath: logURL.path) {
            _ = fm.createFile(atPath: logURL.path, contents: nil)
        }
        if let h = try? FileHandle(forWritingTo: logURL) {
            h.seekToEndOfFile()
            p.standardOutput = h
            p.standardError = h
        }
        do {
            try p.run()
            process = p
            return nil
        } catch {
            return "could not start netpulse: \(error.localizedDescription)"
        }
    }

    /// Stops the server only if this app started it.
    func stop() {
        guard let p = process, p.isRunning else { return }
        p.terminate() // SIGTERM: the Go server shuts down gracefully
        process = nil
    }
}
