import SwiftUI
import DebridKit

/// Web identity in the content layer; navigation and controls retain system materials.
enum DebridStyle {
    static let tint = Color("DeskAccent")
    static let canvas = Color("Chassis")
    static let surface = Color("PanelFace")
    static let edge = Color("PanelEdge")
    static let ink = Color("PanelInk")
    static let secondary = Color("PanelMuted")
    static let orange = Color(red: 1, green: 0.31, blue: 0)
    static let readout = Color(red: 0.114, green: 0.129, blue: 0.106)
    static let readoutText = Color("ReadoutText")
}

struct DebridWordmark: View {
    var body: some View {
        HStack(spacing: 8) {
            Circle().fill(DebridStyle.orange).frame(width: 9, height: 9)
            (Text("DEBRID") + Text(".").foregroundColor(DebridStyle.orange))
                .font(.subheadline.weight(.bold)).tracking(1.5)
        }
        .foregroundStyle(DebridStyle.ink)
        .frame(minHeight: 44, alignment: .leading)
        .contentShape(.accessibility, Rectangle())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Debrid")
    }
}

struct TransferReadout: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("TRANSFER STATUS · DEMO")
                .font(.caption.monospaced().weight(.medium)).tracking(1)
            Text("1 downloading\n1 needs file selection")
                .font(.title3.weight(.semibold)).fixedSize(horizontal: false, vertical: true)
            Text("Sample data · No server connected")
                .font(.footnote).fixedSize(horizontal: false, vertical: true)
        }
        .foregroundColor(DebridStyle.readoutText)
        .padding(20).frame(maxWidth: .infinity, alignment: .leading)
        .background(DebridStyle.readout, in: .rect(cornerRadius: 20))
        .accessibilityElement(children: .combine)
    }
}

extension TransferPreview.Phase {
    var symbol: String {
        switch self {
        case .downloading: "arrow.down"
        case .selecting: "checklist"
        case .completed: "checkmark"
        case .failed: "exclamationmark"
        }
    }

    var ink: Color {
        switch self {
        case .downloading: Color("StageBlue")
        case .selecting: Color("StageAmber")
        case .completed: Color("StageGreen")
        case .failed: Color("DangerInk")
        }
    }

    var lamp: Color {
        switch self {
        case .downloading: Color(red: 0.224, green: 0.761, blue: 1)
        case .selecting: Color(red: 1, green: 0.761, blue: 0.243)
        case .completed: Color(red: 0.349, green: 0.851, blue: 0.478)
        case .failed: Color(red: 1, green: 0.49, blue: 0.4)
        }
    }
}

struct TransferLamp: View {
    let phase: TransferPreview.Phase
    var body: some View {
        Image(systemName: phase.symbol)
            .font(.subheadline.weight(.bold))
            .foregroundColor(Color("LampInk"))
            .frame(width: 32, height: 32)
            .background(phase.lamp, in: .rect(cornerRadius: 10))
            .accessibilityHidden(true)
    }
}

/// A web-family progress motif exposed as one continuous value to assistive technology.
struct TransferSegments: View {
    let fraction: Double
    let color: Color
    private var value: Double { fraction.isFinite ? min(1, max(0, fraction)) : 0 }
    var body: some View {
        HStack(spacing: 3) {
            ForEach(0..<12) { index in
                RoundedRectangle(cornerRadius: 2)
                    .fill(index < Int((value * 12).rounded()) ? color : DebridStyle.edge.opacity(0.5))
            }
        }
        .frame(height: 8)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Download progress")
        .accessibilityValue(value.formatted(.percent.precision(.fractionLength(0))))
    }
}

struct DebridListStyle: ViewModifier {
    func body(content: Content) -> some View {
        content
            .scrollContentBackground(.hidden)
            .background(DebridStyle.canvas)
            .foregroundStyle(DebridStyle.ink)
    }
}

/// Keep the native Button semantics while giving form actions a full touch target.
/// Press feedback is immediate; no custom motion is added to repeated actions.
struct DebridRowActionStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .frame(minWidth: 44, minHeight: 44, alignment: .leading)
            .contentShape(Rectangle())
            .opacity(isEnabled ? (configuration.isPressed ? 0.65 : 1) : 0.4)
    }
}
