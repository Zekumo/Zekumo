import SwiftUI
import ZekumoKit

struct AnnouncementsView: View {
    @Environment(AppModel.self) private var model
    @State private var items: [Announcement] = []
    @State private var loading = true
    @State private var error: String?
    @State private var selection: Announcement.ID?
    @State private var editing: Editing?

    struct Editing: Identifiable {
        var id: String { existing?.id ?? "new" }
        var existing: Announcement?
    }

    var body: some View {
        LoadState(loading: loading && items.isEmpty, error: error, retry: { Task { await load() } }) {
            if items.isEmpty {
                MascotState(mascot: .empty, title: "还没有公告", hint: "公告通过 /v1/announcements 下发给客户端。")
            } else {
                Table(items, selection: $selection) {
                    TableColumn("标题") { a in
                        VStack(alignment: .leading) {
                            Text(a.title).fontWeight(.medium)
                            Text(a.body).lineLimit(1).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    TableColumn("级别") { a in Badge(text: a.importance.label, tone: tone(a.importance)) }.width(60)
                    TableColumn("范围") { Text($0.targetLabel) }.width(110)
                    TableColumn("状态") { a in
                        Badge(text: a.stateLabel, tone: a.active && !a.isExpired ? .ok : .neutral)
                    }
                    .width(70)
                    TableColumn("到期") { Text($0.expiresAt?.full ?? "不过期").foregroundStyle(.secondary) }.width(140)
                    TableColumn("更新") { Text($0.updatedAt.ago).foregroundStyle(.secondary) }.width(100)
                }
                .contextMenu(forSelectionType: Announcement.ID.self) { ids in
                    if let a = items.first(where: { $0.id == ids.first }) {
                        Button("编辑…") { editing = Editing(existing: a) }.disabled(!model.canWrite)
                        if a.active {
                            Button("下架…", role: .destructive) { deactivate(a) }.disabled(!model.canWrite)
                        }
                    }
                } primaryAction: { ids in
                    if model.canWrite, let a = items.first(where: { $0.id == ids.first }) { editing = Editing(existing: a) }
                }
            }
        }
        .toolbar {
            ToolbarItem {
                Button { editing = Editing() } label: { Label("新建公告", systemImage: "plus") }
                    .disabled(!model.canWrite)
            }
        }
        .task(id: model.loadKey) { await load() }
        .sheet(item: $editing) { AnnouncementSheet(existing: $0.existing) }
    }

    private func tone(_ i: Importance) -> Badge.Tone {
        switch i {
        case .info: .neutral
        case .warning: .warn
        case .critical: .error
        }
    }

    private func load() async {
        guard let client = model.client, let gameID = model.gameID else { return }
        loading = true
        defer { loading = false }
        do {
            items = try await client.announcements(gameID: gameID)
            error = nil
        } catch is CancellationError {
        } catch {
            if case ZekumoError.unauthorized = error { model.report(error) }
            self.error = error.localizedDescription
        }
    }

    private func deactivate(_ a: Announcement) {
        model.confirm("下架「\(a.title)」?", message: "客户端将不再收到这条公告。",
                      label: "下架", success: "已下架") { [client = model.client] in
            try await client?.deactivateAnnouncement(id: a.id)
        }
    }
}

private struct AnnouncementSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var existing: Announcement?
    @State private var title = ""
    @State private var text = ""
    @State private var importance = Importance.info
    @State private var platform = ""
    @State private var channel = ""
    @State private var expiry = Expiry.keep
    @State private var hours = 24
    @State private var busy = false

    enum Expiry: Hashable { case keep, never, after }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(existing == nil ? "新建公告" : "编辑公告").font(.title2.weight(.semibold))
            HStack(alignment: .top, spacing: 20) {
                Form {
                    TextField("标题", text: $title)
                    Picker("级别", selection: $importance) {
                        ForEach(Importance.allCases, id: \.self) { Text($0.label).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    TextField("平台", text: $platform, prompt: Text("留空为全部"))
                    TextField("渠道", text: $channel, prompt: Text("留空为全部"))
                    Picker("到期", selection: $expiry) {
                        if existing != nil { Text("保持不变").tag(Expiry.keep) }
                        Text("不过期").tag(Expiry.never)
                        Text("一段时间后").tag(Expiry.after)
                    }
                    if expiry == .after {
                        Stepper("\(hours) 小时后", value: $hours, in: 1...24 * 90)
                    }
                    LabeledContent("正文") {
                        TextEditor(text: $text).font(.body).frame(height: 160)
                            .overlay(RoundedRectangle(cornerRadius: 4).stroke(.separator))
                    }
                }
                .frame(width: 400)
                VStack(alignment: .leading, spacing: 8) {
                    Text("预览").font(.caption).foregroundStyle(.secondary)
                    VStack(alignment: .leading, spacing: 8) {
                        HStack {
                            Badge(text: importance.label, tone: importance == .critical ? .error : importance == .warning ? .warn : .neutral)
                            Text(title.isEmpty ? "公告标题" : title).font(.headline)
                        }
                        ScrollView {
                            Text((try? AttributedString(markdown: text, options: .init(
                                interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(text))
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }
                    .padding(12)
                    .frame(width: 260, height: 280, alignment: .topLeading)
                    .background(.background.secondary, in: RoundedRectangle(cornerRadius: 10))
                }
            }
            HStack {
                Spacer()
                Button("取消", role: .cancel) { dismiss() }.keyboardShortcut(.cancelAction)
                Button(existing == nil ? "发布" : "保存", action: submit).keyboardShortcut(.defaultAction)
                    .disabled(busy || title.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(24)
        .onAppear(perform: fill)
    }

    private func fill() {
        guard let a = existing else {
            expiry = .never
            return
        }
        title = a.title
        text = a.body
        importance = a.importance
        platform = a.platform
        channel = a.channel
    }

    private func submit() {
        guard let client = model.client, let gameID = model.gameID else { return }
        var input = AnnouncementInput(title: title.trimmingCharacters(in: .whitespaces), body: text,
                                      importance: importance,
                                      platform: platform.trimmingCharacters(in: .whitespaces),
                                      channel: channel.trimmingCharacters(in: .whitespaces))
        switch expiry {
        case .keep: break
        case .never: input.clearExpires = existing != nil ? true : nil
        case .after: input.expiresInSecs = hours * 3600
        }
        busy = true
        Task {
            defer { busy = false }
            do {
                if let existing {
                    try await client.updateAnnouncement(id: existing.id, input)
                } else {
                    try await client.createAnnouncement(gameID: gameID, input)
                }
                model.flash(existing == nil ? "公告已发布" : "公告已保存")
                model.refresh()
                dismiss()
            } catch {
                model.report(error)
            }
        }
    }
}
