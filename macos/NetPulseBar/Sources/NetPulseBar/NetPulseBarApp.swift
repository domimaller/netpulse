import AppKit
import SwiftUI

/// Menu bar app for netpulse: a coloured status dot (deep green = excellent …
/// red = bad/down) that opens a panel with the live numbers. It uses an
/// already running netpulse server, or starts the one bundled in the app.
@main
struct NetPulseBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var store = Store.shared

    var body: some Scene {
        MenuBarExtra {
            PanelView()
                .environmentObject(store)
        } label: {
            MenuBarLabel(store: store)
        }
        .menuBarExtraStyle(.window)
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationWillTerminate(_ notification: Notification) {
        // Stop the server only if this app started it.
        Store.shared.shutdown()
    }
}
