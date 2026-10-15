import AppKit
import SwiftUI

/// Colours for each quality level (deep green = best).
enum Palette {
    static func nsColor(_ level: Level) -> NSColor {
        switch level {
        case .excellent: return NSColor(srgbRed: 0.05, green: 0.55, blue: 0.24, alpha: 1) // deep green
        case .good: return NSColor(srgbRed: 0.30, green: 0.80, blue: 0.36, alpha: 1)      // green
        case .fair: return NSColor(srgbRed: 0.98, green: 0.78, blue: 0.00, alpha: 1)      // yellow
        case .poor: return NSColor(srgbRed: 1.00, green: 0.55, blue: 0.10, alpha: 1)      // orange
        case .bad, .down: return NSColor(srgbRed: 0.90, green: 0.22, blue: 0.21, alpha: 1) // red
        case .offline: return .secondaryLabelColor
        }
    }

    static func color(_ level: Level) -> Color { Color(nsColor: nsColor(level)) }

    static func status(_ status: String) -> Color {
        switch status {
        case "down": return color(.bad)
        case "degraded": return color(.fair)
        default: return color(.good)
        }
    }
}

/// Draws the menu bar item: a coloured dot, optionally followed by text.
/// The image is deliberately *not* a template image, otherwise macOS would
/// render it monochrome and the colour would be lost.
enum StatusIcon {
    static func image(level: Level, text: String?) -> NSImage {
        let font = NSFont.monospacedDigitSystemFont(ofSize: 13, weight: .regular)
        let attrs: [NSAttributedString.Key: Any] = [
            .font: font,
            .foregroundColor: NSColor.labelColor, // resolved at draw time → follows menu bar appearance
        ]
        let textSize = text.map { ($0 as NSString).size(withAttributes: attrs) } ?? .zero
        let dot: CGFloat = 12
        let gap: CGFloat = text == nil ? 0 : 5
        let height: CGFloat = 18
        let size = NSSize(width: ceil(dot + gap + textSize.width) + 1, height: height)

        let image = NSImage(size: size, flipped: false) { _ in
            let dotRect = NSRect(x: 0.5, y: (height - dot) / 2, width: dot, height: dot)
            let color = Palette.nsColor(level)
            if level == .offline {
                color.setStroke()
                let ring = NSBezierPath(ovalIn: dotRect.insetBy(dx: 1, dy: 1))
                ring.lineWidth = 1.5
                ring.stroke()
            } else {
                color.setFill()
                NSBezierPath(ovalIn: dotRect).fill()
                if level == .down { // white "x" so "down" is not colour-only
                    let r = dotRect.insetBy(dx: 3.5, dy: 3.5)
                    let x = NSBezierPath()
                    x.move(to: NSPoint(x: r.minX, y: r.minY))
                    x.line(to: NSPoint(x: r.maxX, y: r.maxY))
                    x.move(to: NSPoint(x: r.minX, y: r.maxY))
                    x.line(to: NSPoint(x: r.maxX, y: r.minY))
                    x.lineWidth = 1.6
                    NSColor.white.setStroke()
                    x.stroke()
                }
            }
            if let text {
                (text as NSString).draw(
                    at: NSPoint(x: dot + gap, y: (height - textSize.height) / 2),
                    withAttributes: attrs)
            }
            return true
        }
        image.isTemplate = false
        image.accessibilityDescription = "netpulse: \(level.title)" + (text.map { ", \($0)" } ?? "")
        return image
    }
}

/// The view placed in the menu bar.
struct MenuBarLabel: View {
    @ObservedObject var store: Store

    private var text: String? {
        guard store.showLatency, store.reachable, let now = store.summary?.now else {
            return nil
        }
        return now.isDown ? "down" : "\(Fmt.ms(now.rttMs)) ms"
    }

    var body: some View {
        Image(nsImage: StatusIcon.image(level: store.level, text: text))
            .renderingMode(.original)
    }
}
