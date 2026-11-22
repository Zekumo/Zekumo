import AppKit
import SwiftUI
import ZekumoKit

enum Assets {
    /// The packaged .app keeps SwiftPM's resource bundle in Contents/Resources;
    /// `swift run` finds it next to the binary through Bundle.module.
    static let bundle: Bundle = {
        if let url = Bundle.main.url(forResource: "ZekumoConsole_ZekumoConsole", withExtension: "bundle"),
           let bundle = Bundle(url: url) {
            return bundle
        }
        return .module
    }()
}

enum Mascot: String {
    case welcome = "zekumo-welcome-wave"
    case empty = "zekumo-empty-cloud"
    case error = "zekumo-error-concerned"

    var image: Image {
        if let url = Assets.bundle.url(forResource: rawValue, withExtension: "png"),
           let image = NSImage(contentsOf: url) {
            return Image(nsImage: image)
        }
        return Image(systemName: "cloud")
    }
}

struct MascotState: View {
    var mascot: Mascot
    var title: String
    var hint: String = ""
    var retry: (() -> Void)?

    var body: some View {
        VStack(spacing: 12) {
            mascot.image.resizable().scaledToFit().frame(width: 140, height: 140)
            Text(title).font(.title3.weight(.semibold))
            if !hint.isEmpty {
                Text(hint).foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 420)
            }
            if let retry { Button("重试", action: retry) }
        }
        .padding(32)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// Loading / error / content wrapper every screen uses.
struct LoadState<Content: View>: View {
    var loading: Bool
    var error: String?
    var retry: () -> Void
    @ViewBuilder var content: () -> Content

    var body: some View {
        if let error {
            MascotState(mascot: .error, title: "加载失败", hint: error, retry: retry)
        } else if loading {
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            content()
        }
    }
}

struct Badge: View {
    enum Tone { case neutral, ok, warn, error }
    var text: String
    var tone: Tone = .neutral

    private var color: Color {
        switch tone {
        case .neutral: .secondary
        case .ok: .green
        case .warn: .orange
        case .error: .red
        }
    }

    var body: some View {
        Text(text)
            .font(.caption.weight(.medium))
            .padding(.horizontal, 6).padding(.vertical, 2)
            .foregroundStyle(color)
            .background(color.opacity(0.12), in: Capsule())
    }
}

struct Tile: View {
    var title: String
    var value: String
    var detail: String = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title).font(.subheadline).foregroundStyle(.secondary)
            Text(value).font(.system(size: 28, weight: .semibold, design: .rounded)).monospacedDigit()
            Text(detail.isEmpty ? " " : detail).font(.caption).foregroundStyle(.secondary).lineLimit(1)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(16)
        .background(.background.secondary, in: RoundedRectangle(cornerRadius: 12))
    }
}

struct Card<Content: View>: View {
    var title: String
    var trailing: AnyView?
    @ViewBuilder var content: () -> Content

    init(_ title: String, trailing: AnyView? = nil, @ViewBuilder content: @escaping () -> Content) {
        self.title = title
        self.trailing = trailing
        self.content = content
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(title).font(.headline)
                Spacer()
                trailing
            }
            content()
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.background.secondary, in: RoundedRectangle(cornerRadius: 12))
    }
}

/// A credential with a copy button; secrets stay masked until revealed, so a
/// screen share does not leak every key at once.
struct CopyField: View {
    @Environment(AppModel.self) private var model
    var value: String
    var label: String
    var secret = false
    @State private var revealed = false

    var body: some View {
        HStack(spacing: 4) {
            Text(secret && !revealed ? String(repeating: "•", count: 16) : value)
                .font(.system(.body, design: .monospaced))
                .lineLimit(1)
                .truncationMode(.middle)
                .textSelection(.enabled)
            if secret {
                Button { revealed.toggle() } label: { Image(systemName: revealed ? "eye.slash" : "eye") }
                    .buttonStyle(.borderless).help(revealed ? "隐藏" : "显示")
            }
            Button {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(value, forType: .string)
                model.flash("已复制 \(label)")
            } label: { Image(systemName: "doc.on.doc") }
                .buttonStyle(.borderless).help("复制")
        }
    }
}

struct SecurityBanner: View {
    var warnings: [String]

    var body: some View {
        if !warnings.isEmpty {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: "exclamationmark.shield.fill").foregroundStyle(.red).font(.title3)
                VStack(alignment: .leading, spacing: 2) {
                    Text("部署尚未加固").font(.headline)
                    Text(warnings.map(Self.describe).joined(separator: ";"))
                        .font(.callout).foregroundStyle(.secondary)
                }
                Spacer()
            }
            .padding(12)
            .background(Color.red.opacity(0.1), in: RoundedRectangle(cornerRadius: 10))
        }
    }

    static func describe(_ warning: String) -> String {
        switch warning {
        case "default_password": "仍在使用默认控制台密码 admin123"
        case "default_jwt_secret": "JWT_SECRET 是默认值,任何人都能伪造令牌"
        default: warning
        }
    }
}

extension Date {
    var ago: String { formatted(.relative(presentation: .named)) }
    var full: String { formatted(date: .numeric, time: .shortened) }
}

func formatBytes(_ n: Int64) -> String {
    ByteCountFormatter.string(fromByteCount: n, countStyle: .file)
}

func percent(_ part: Int, _ whole: Int) -> String {
    whole == 0 ? "—" : String(format: "%.0f%%", Double(part) / Double(whole) * 100)
}
