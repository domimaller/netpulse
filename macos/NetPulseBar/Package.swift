// swift-tools-version:5.9
import PackageDescription

// Menu bar companion for netpulse. Build the full app bundle with `make app`
// from the repository root; `swift build` alone only builds the executable.
let package = Package(
    name: "NetPulseBar",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(name: "NetPulseBar", path: "Sources/NetPulseBar")
    ]
)
