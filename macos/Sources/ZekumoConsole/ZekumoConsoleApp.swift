import AppKit
import SwiftUI

@main
struct ZekumoConsoleApp: App {
    @State private var model = AppModel()

    init() {
        // Running straight from `swift run` has no bundle telling AppKit this is a GUI app.
        NSApplication.shared.setActivationPolicy(.regular)
    }

    var body: some Scene {
        WindowGroup("Zekumo 控制台") {
            RootView()
                .environment(model)
                .environment(\.locale, Locale(identifier: "zh-Hans"))
                .frame(minWidth: 980, minHeight: 640)
                .task {
                    NSApplication.shared.activate()
                    await model.restore()
                }
        }
        .windowToolbarStyle(.unified)
        .commands {
            CommandGroup(replacing: .newItem) {}
            CommandMenu("控制台") {
                Button("刷新") { model.refresh() }
                    .keyboardShortcut("r")
                    .disabled(!model.isSignedIn)
                Divider()
                Button("退出登录") { model.logout() }
                    .keyboardShortcut("q", modifiers: [.command, .shift])
                    .disabled(!model.isSignedIn)
            }
        }
    }
}
