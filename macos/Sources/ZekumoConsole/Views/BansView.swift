import SwiftUI
import ZekumoKit

struct BansView: View {
    @Environment(AppModel.self) private var model
    @State private var bans: [PlayerBan] = []
    @State private var loading = true
    @State private var error: String?
    @State private var selection: PlayerBan.ID?
    @State private var showLifted = false
    @State private var banning = false

    private var visible: [PlayerBan] { showLifted ? bans : bans.filter(\.isActive) }

    var body: some View {
        LoadState(loading: loading && bans.isEmpty, error: error, retry: { Task { await load() } }) {
            if visible.isEmpty {
                MascotState(mascot: .empty, title: "没有生效中的封禁", hint: "风平浪静。")
            } else {
                Table(visible, selection: $selection) {
                    TableColumn("玩家") { b in
                        VStack(alignment: .leading) {
                            Text(b.nickname.flatMap { $0.isEmpty ? nil : $0 } ?? "未命名")
                            Text(b.playerId).font(.caption.monospaced()).foregroundStyle(.secondary)
                        }
                    }
                    TableColumn("原因") { Text($0.reason.isEmpty ? "—" : $0.reason) }
                    TableColumn("到期") { b in
                        Text(b.expiresAt.map(\.full) ?? "永久")
                            .foregroundStyle(b.expiresAt == nil ? .red : .primary)
                    }
                    .width(140)
                    TableColumn("状态") { b in
                        if b.liftedAt != nil {
                            Badge(text: "已解除")
                        } else if b.isActive {
                            Badge(text: "生效中", tone: .error)
                        } else {
                            Badge(text: "已到期")
                        }
                    }
                    .width(70)
                    TableColumn("操作人") { Text($0.operator_ ?? "—").foregroundStyle(.secondary) }.width(100)
                    TableColumn("时间") { Text($0.createdAt.ago).foregroundStyle(.secondary) }.width(100)
                }
                .contextMenu(forSelectionType: PlayerBan.ID.self) { ids in
                    if let b = bans.first(where: { $0.id == ids.first }), b.isActive {
                        Button("解除封禁") { unban(b) }.disabled(!model.canWrite)
                    }
                }
            }
        }
        .toolbar {
            ToolbarItemGroup {
                Toggle("显示已解除", isOn: $showLifted).toggleStyle(.checkbox)
                Button { banning = true } label: { Label("封禁玩家", systemImage: "nosign") }
                    .disabled(!model.canWrite)
            }
        }
        .task(id: model.loadKey) { await load() }
        .sheet(isPresented: $banning) { BanSheet(player: nil) }
    }

    private func load() async {
        guard let client = model.client, let gameID = model.gameID else { return }
        loading = true
        defer { loading = false }
        do {
            bans = try await client.bans(gameID: gameID)
            error = nil
        } catch is CancellationError {
        } catch {
            if case ZekumoError.unauthorized = error { model.report(error) }
            self.error = error.localizedDescription
        }
    }

    private func unban(_ b: PlayerBan) {
        model.confirm("解除对「\(b.nickname ?? b.playerId)」的封禁?", label: "解除封禁", destructive: false,
                      success: "已解除封禁") { [client = model.client] in
            try await client?.unban(playerID: b.playerId)
        }
    }
}

struct BanSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var player: Player?
    @State private var playerID = ""
    @State private var reason = ""
    @State private var duration: Int64 = 86400
    @State private var busy = false

    static let durations: [(String, Int64)] = [
        ("1 小时", 3600), ("1 天", 86400), ("7 天", 7 * 86400), ("30 天", 30 * 86400), ("永久", 0),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(player.map { "封禁「\($0.nickname.isEmpty ? "未命名" : $0.nickname)」" } ?? "封禁玩家")
                .font(.title2.weight(.semibold))
            Form {
                if player == nil {
                    TextField("玩家 ID", text: $playerID).font(.body.monospaced())
                }
                TextField("原因", text: $reason, prompt: Text("玩家登录时会看到"))
                Picker("时长", selection: $duration) {
                    ForEach(Self.durations, id: \.1) { Text($0.0).tag($0.1) }
                }
                .pickerStyle(.segmented)
            }
            Text("封禁后玩家的现有会话立即失效。").font(.callout).foregroundStyle(.secondary)
            HStack {
                Spacer()
                Button("取消", role: .cancel) { dismiss() }.keyboardShortcut(.cancelAction)
                Button("封禁", role: .destructive, action: submit)
                    .keyboardShortcut(.defaultAction)
                    .disabled(busy || targetID.isEmpty)
            }
        }
        .padding(24).frame(width: 460)
    }

    private var targetID: String { player?.id ?? playerID.trimmingCharacters(in: .whitespaces) }

    private func submit() {
        guard let client = model.client, !targetID.isEmpty else { return }
        busy = true
        Task {
            defer { busy = false }
            do {
                try await client.ban(playerID: targetID, reason: reason, durationSecs: duration)
                model.flash("已封禁")
                model.refresh()
                dismiss()
            } catch {
                model.report(error)
            }
        }
    }
}
