import SwiftUI
import ZekumoKit

struct GamesView: View {
    @Environment(AppModel.self) private var model
    @State private var selection: Game.ID?
    @State private var creating = false

    var body: some View {
        Group {
            if model.games.isEmpty {
                MascotState(mascot: .empty, title: "还没有游戏",
                            hint: model.canManage ? "新建一个游戏,拿到 App ID 和 Secret 接入 SDK。" : "请联系管理员创建游戏。")
            } else {
                Table(model.games, selection: $selection) {
                    TableColumn("名称") { game in
                        HStack {
                            Text(game.name).fontWeight(.medium)
                            if game.status != "active" { Badge(text: game.status, tone: .warn) }
                        }
                    }
                    TableColumn("App ID") { CopyField(value: $0.appId, label: "App ID") }
                    TableColumn("App Secret") { game in
                        if let secret = game.appSecret, !secret.isEmpty {
                            CopyField(value: secret, label: "App Secret", secret: true)
                        } else {
                            Text("无权限查看").foregroundStyle(.tertiary)
                        }
                    }
                    TableColumn("在线") { Text("\($0.online ?? 0)").monospacedDigit() }.width(50)
                    TableColumn("创建") { Text($0.createdAt.full).foregroundStyle(.secondary) }.width(140)
                }
                .contextMenu(forSelectionType: Game.ID.self) { ids in
                    if let game = model.games.first(where: { $0.id == ids.first }) {
                        Button("查看玩家") { model.open(game: game) }
                        Button("版本更新") { model.open(game: game, page: .releases) }
                        Button("公告") { model.open(game: game, page: .announcements) }
                        Divider()
                        Button("删除游戏…", role: .destructive) { delete(game) }
                            .disabled(!model.isOwner)
                    }
                } primaryAction: { ids in
                    if let game = model.games.first(where: { $0.id == ids.first }) { model.open(game: game) }
                }
            }
        }
        .toolbar {
            ToolbarItem {
                Button { creating = true } label: { Label("新建游戏", systemImage: "plus") }
                    .disabled(!model.canManage)
                    .help(model.canManage ? "新建游戏" : "需要管理员权限")
            }
        }
        .sheet(isPresented: $creating) { NewGameSheet() }
    }

    private func delete(_ game: Game) {
        model.confirm("删除「\(game.name)」?",
                      message: "玩家、存档、版本和公告会一并删除,无法恢复。",
                      label: "删除", success: "已删除 \(game.name)") { [client = model.client] in
            try await client?.deleteGame(id: game.id)
        }
    }
}

private struct NewGameSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var busy = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("新建游戏").font(.title2.weight(.semibold))
            TextField("游戏名称", text: $name).onSubmit(create)
            HStack {
                Spacer()
                Button("取消", role: .cancel) { dismiss() }.keyboardShortcut(.cancelAction)
                Button("创建", action: create).keyboardShortcut(.defaultAction)
                    .disabled(busy || name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(24).frame(width: 380)
    }

    private func create() {
        let name = name.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty, let client = model.client else { return }
        busy = true
        Task {
            defer { busy = false }
            do {
                let game = try await client.createGame(name: name)
                await model.reloadGames()
                model.gameID = game.id
                model.flash("已创建 \(game.name)")
                dismiss()
            } catch {
                model.report(error)
            }
        }
    }
}
