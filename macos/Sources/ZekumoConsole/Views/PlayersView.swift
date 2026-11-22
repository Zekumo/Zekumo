import SwiftUI
import ZekumoKit

struct PlayersView: View {
    @Environment(AppModel.self) private var model
    @State private var players: [Player] = []
    @State private var loading = true
    @State private var error: String?
    @State private var search = ""
    @State private var selection: Player.ID?
    @State private var inspecting: Player?
    @State private var banning: Player?

    var body: some View {
        LoadState(loading: loading && players.isEmpty, error: error, retry: { Task { await load() } }) {
            if players.isEmpty {
                MascotState(mascot: .empty, title: search.isEmpty ? "还没有玩家" : "没有匹配的玩家",
                            hint: search.isEmpty ? "玩家通过 SDK 登录后会出现在这里。" : "换个昵称或标识试试。")
            } else {
                table
            }
        }
        .searchable(text: $search, prompt: "昵称或标识")
        .task(id: "\(model.loadKey)|\(search)") {
            if !search.isEmpty { try? await Task.sleep(for: .milliseconds(300)) }
            await load()
        }
        .sheet(item: $inspecting) { SaveDataSheet(player: $0) }
        .sheet(item: $banning) { BanSheet(player: $0) }
    }

    private var table: some View {
        Table(players, selection: $selection) {
            TableColumn("昵称") { p in
                HStack(spacing: 6) {
                    Text(p.nickname.isEmpty ? "未命名" : p.nickname)
                        .foregroundStyle(p.nickname.isEmpty ? .secondary : .primary)
                    if p.banned { Badge(text: "封禁中", tone: .error) }
                    if p.accountId != nil { Badge(text: "已绑定通行证") }
                }
            }
            TableColumn("方式") { Text($0.providerLabel) }.width(60)
            TableColumn("标识") { p in
                Text(p.identifier).font(.system(.body, design: .monospaced)).lineLimit(1).truncationMode(.middle)
                    .textSelection(.enabled)
            }
            TableColumn("注册") { Text($0.createdAt.full).foregroundStyle(.secondary) }.width(140)
            TableColumn("最近登录") { Text($0.lastLoginAt.ago).foregroundStyle(.secondary) }.width(110)
        }
        .contextMenu(forSelectionType: Player.ID.self) { ids in
            if let p = players.first(where: { $0.id == ids.first }) {
                Button("查看存档") { inspecting = p }
                Divider()
                if p.banned {
                    Button("解除封禁") { unban(p) }.disabled(!model.canWrite)
                } else {
                    Button("封禁…", role: .destructive) { banning = p }.disabled(!model.canWrite)
                }
            }
        } primaryAction: { ids in
            inspecting = players.first { $0.id == ids.first }
        }
    }

    private func load() async {
        guard let client = model.client, let gameID = model.gameID else { return }
        loading = true
        defer { loading = false }
        do {
            players = try await client.players(gameID: gameID, search: search)
            error = nil
        } catch is CancellationError {
        } catch {
            if case ZekumoError.unauthorized = error { model.report(error) }
            self.error = error.localizedDescription
        }
    }

    private func unban(_ p: Player) {
        model.confirm("解除对「\(p.nickname)」的封禁?", label: "解除封禁", destructive: false,
                      success: "已解除封禁") { [client = model.client] in
            try await client?.unban(playerID: p.id)
        }
    }
}

struct SaveDataSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var player: Player
    @State private var entries: [SaveEntry] = []
    @State private var loading = true
    @State private var error: String?
    @State private var selected: SaveEntry.ID?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                VStack(alignment: .leading) {
                    Text(player.nickname.isEmpty ? "未命名玩家" : player.nickname).font(.title2.weight(.semibold))
                    Text(player.id).font(.caption.monospaced()).foregroundStyle(.secondary).textSelection(.enabled)
                }
                Spacer()
                Button("完成") { dismiss() }.keyboardShortcut(.defaultAction)
            }
            .padding(20)
            Divider()
            LoadState(loading: loading, error: error, retry: { Task { await load() } }) {
                if entries.isEmpty {
                    MascotState(mascot: .empty, title: "没有存档数据")
                } else {
                    HSplitView {
                        List(entries, selection: $selected) { e in
                            VStack(alignment: .leading) {
                                Text(e.key).font(.body.monospaced())
                                Text(e.updatedAt.ago).font(.caption).foregroundStyle(.secondary)
                            }
                        }
                        .frame(minWidth: 180, idealWidth: 200)
                        ScrollView {
                            Text(entries.first { $0.id == selected }?.value?.prettyPrinted ?? "null")
                                .font(.body.monospaced())
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(16)
                        }
                        .frame(minWidth: 320)
                    }
                }
            }
        }
        .frame(width: 720, height: 480)
        .task { await load() }
    }

    private func load() async {
        guard let client = model.client else { return }
        loading = true
        defer { loading = false }
        do {
            entries = try await client.playerData(playerID: player.id)
            selected = selected ?? entries.first?.id
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }
}
