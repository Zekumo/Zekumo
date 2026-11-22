import SwiftUI
import ZekumoKit

struct RootView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        @Bindable var model = model
        Group {
            if model.restoring {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if model.isSignedIn {
                ConsoleView()
            } else {
                LoginView()
            }
        }
        .overlay(alignment: .bottom) {
            if let toast = model.toast {
                Text(toast)
                    .padding(.horizontal, 14).padding(.vertical, 8)
                    .background(.regularMaterial, in: Capsule())
                    .shadow(radius: 4)
                    .padding(.bottom, 20)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .animation(.snappy, value: model.toast)
        .alert("出错了", isPresented: Binding(get: { model.errorMessage != nil },
                                            set: { if !$0 { model.errorMessage = nil } })) {
            Button("好") {}
        } message: {
            Text(model.errorMessage ?? "")
        }
        .alert(model.confirmation?.title ?? "", isPresented: Binding(get: { model.confirmation != nil },
                                                                     set: { if !$0 { model.confirmation = nil } }),
               presenting: model.confirmation) { pending in
            Button(pending.confirmLabel, role: pending.destructive ? .destructive : nil) {
                model.perform(nil, pending.action)
            }
            Button("取消", role: .cancel) {}
        } message: { pending in
            Text(pending.message)
        }
    }
}

struct ConsoleView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        @Bindable var model = model
        NavigationSplitView {
            List(selection: $model.page) {
                Section("工作区") {
                    ForEach([Page.dashboard, .games], id: \.self) { page in
                        Label(page.title, systemImage: page.symbol).tag(page)
                    }
                }
                Section("当前游戏") {
                    Picker("游戏", selection: $model.gameID) {
                        Text("选择游戏…").tag(String?.none)
                        ForEach(model.games) { game in
                            Text(game.name).tag(Optional(game.id))
                        }
                    }
                    .labelsHidden()
                    .pickerStyle(.menu)
                    ForEach([Page.players, .releases, .announcements, .bans], id: \.self) { page in
                        Label(page.title, systemImage: page.symbol)
                            .tag(page)
                            .selectionDisabled(model.gameID == nil)
                            .foregroundStyle(model.gameID == nil ? .tertiary : .primary)
                    }
                }
            }
            .navigationSplitViewColumnWidth(min: 200, ideal: 220)
            .safeAreaInset(edge: .bottom) { AccountFooter() }
        } detail: {
            Group {
                switch model.page ?? .dashboard {
                case .dashboard: DashboardView()
                case .games: GamesView()
                case .players: PlayersView()
                case .releases: ReleasesView()
                case .announcements: AnnouncementsView()
                case .bans: BansView()
                }
            }
            .navigationTitle(title)
            .navigationSubtitle(subtitle)
        }
        .toolbar {
            ToolbarItem(placement: .navigation) {
                Picker("工作区", selection: $model.workspaceID) {
                    ForEach(model.workspaces) { ws in
                        Text("\(ws.organizationName) / \(ws.name)").tag(Optional(ws.id))
                    }
                }
                .help("切换工作区")
                .disabled(model.workspaces.count < 2)
            }
            ToolbarItem(placement: .primaryAction) {
                Button { model.refresh() } label: { Label("刷新", systemImage: "arrow.clockwise") }
                    .help("刷新 (⌘R)")
            }
        }
    }

    private var title: String { (model.page ?? .dashboard).title }

    private var subtitle: String {
        if (model.page ?? .dashboard).isGameScoped, let game = model.game { return game.name }
        return model.workspace?.name ?? ""
    }
}

private struct AccountFooter: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "person.crop.circle.fill").font(.title2).foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 1) {
                Text(model.me?.username ?? "").font(.callout.weight(.medium)).lineLimit(1)
                Text(model.role.label).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            Button { model.logout() } label: { Image(systemName: "rectangle.portrait.and.arrow.right") }
                .buttonStyle(.borderless)
                .help("退出登录")
        }
        .padding(12)
    }
}
