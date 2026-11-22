import SwiftUI
import UniformTypeIdentifiers
import ZekumoKit

struct ReleasesView: View {
    @Environment(AppModel.self) private var model
    @State private var releases: [Release] = []
    @State private var loading = true
    @State private var error: String?
    @State private var selection: Release.ID?
    @State private var channel = "全部"
    @State private var creating = false
    @State private var artifactsFor: Release?
    @State private var rolloutFor: Release?

    private var channels: [String] { ["全部"] + Array(Set(releases.map(\.channel))).sorted() }
    private var visible: [Release] { channel == "全部" ? releases : releases.filter { $0.channel == channel } }

    var body: some View {
        LoadState(loading: loading && releases.isEmpty, error: error, retry: { Task { await load() } }) {
            if visible.isEmpty {
                MascotState(mascot: .empty, title: "还没有版本",
                            hint: "新建版本 → 上传产物 → 发布。客户端通过 /v1/updates/check 拿到更新。")
            } else {
                table
            }
        }
        .toolbar {
            ToolbarItemGroup {
                Picker("渠道", selection: $channel) {
                    ForEach(channels, id: \.self) { Text($0).tag($0) }
                }
                Button { creating = true } label: { Label("新建版本", systemImage: "plus") }
                    .disabled(!model.canWrite)
            }
        }
        .task(id: model.loadKey) { await load() }
        .sheet(isPresented: $creating) { NewReleaseSheet { artifactsFor = $0 } }
        .sheet(item: $artifactsFor) { ArtifactsSheet(release: $0) }
        .sheet(item: $rolloutFor) { RolloutSheet(release: $0) }
    }

    private var table: some View {
        Table(visible, selection: $selection) {
            TableColumn("版本") { r in
                HStack(spacing: 6) {
                    Text(r.version).font(.body.monospaced().weight(.medium))
                    if r.mandatory { Badge(text: "强制", tone: .warn) }
                }
            }
            TableColumn("渠道") { Text($0.channel) }.width(70)
            TableColumn("状态") { r in Badge(text: r.status.label, tone: tone(r.status)) }.width(70)
            TableColumn("灰度") { r in
                if r.status == .published {
                    ProgressView(value: Double(r.rolloutPercent), total: 100) { EmptyView() } currentValueLabel: {
                        Text("\(r.rolloutPercent)%").monospacedDigit()
                    }
                } else {
                    Text("—").foregroundStyle(.tertiary)
                }
            }
            .width(110)
            TableColumn("更新说明") { Text($0.changelog).lineLimit(1).foregroundStyle(.secondary) }
            TableColumn("发布时间") { Text($0.publishedAt?.full ?? "—").foregroundStyle(.secondary) }.width(140)
        }
        .contextMenu(forSelectionType: Release.ID.self) { ids in
            if let r = releases.first(where: { $0.id == ids.first }) { menu(r) }
        } primaryAction: { ids in
            artifactsFor = releases.first { $0.id == ids.first }
        }
    }

    @ViewBuilder
    private func menu(_ r: Release) -> some View {
        Button("产物…") { artifactsFor = r }
        Divider()
        Group {
            if r.status == .draft {
                Button("发布") { publish(r) }
            }
            if r.status == .published {
                Button("调整灰度…") { rolloutFor = r }
                Button("下架…", role: .destructive) { revoke(r) }
            }
            if r.status == .draft {
                Button("删除…", role: .destructive) { delete(r) }
            }
        }
        .disabled(!model.canWrite)
    }

    private func tone(_ s: ReleaseStatus) -> Badge.Tone {
        switch s {
        case .draft: .neutral
        case .published: .ok
        case .deprecated: .warn
        case .revoked: .error
        }
    }

    private func load() async {
        guard let client = model.client, let gameID = model.gameID else { return }
        loading = true
        defer { loading = false }
        do {
            releases = try await client.releases(gameID: gameID)
            if !channels.contains(channel) { channel = "全部" }
            error = nil
        } catch is CancellationError {
        } catch {
            if case ZekumoError.unauthorized = error { model.report(error) }
            self.error = error.localizedDescription
        }
    }

    private func publish(_ r: Release) {
        guard let gameID = model.gameID else { return }
        model.confirm("发布 \(r.version)(\(r.channel))?",
                      message: "当前渠道里旧的已发布版本会被标记为弃用。发布前至少要有一个上传完成的产物。",
                      label: "发布", destructive: false, success: "已发布 \(r.version)") { [client = model.client] in
            try await client?.publishRelease(gameID: gameID, version: r.version, channel: r.channel)
        }
    }

    private func revoke(_ r: Release) {
        guard let gameID = model.gameID else { return }
        model.confirm("下架 \(r.version)?", message: "客户端将不再收到这个版本。",
                      label: "下架", success: "已下架 \(r.version)") { [client = model.client] in
            try await client?.revokeRelease(gameID: gameID, version: r.version, channel: r.channel)
        }
    }

    private func delete(_ r: Release) {
        guard let gameID = model.gameID else { return }
        model.confirm("删除 \(r.version)?", message: "草稿和已上传的产物文件都会被删除。",
                      label: "删除", success: "已删除 \(r.version)") { [client = model.client] in
            try await client?.deleteRelease(gameID: gameID, version: r.version, channel: r.channel)
        }
    }
}

private struct NewReleaseSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var created: (Release) -> Void
    @State private var version = ""
    @State private var channel = "stable"
    @State private var changelog = ""
    @State private var minSupported = ""
    @State private var mandatory = false
    @State private var busy = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("新建版本").font(.title2.weight(.semibold))
            Form {
                TextField("版本号", text: $version, prompt: Text("1.2.0"))
                TextField("渠道", text: $channel)
                TextField("最低支持版本", text: $minSupported, prompt: Text("可选,低于它的客户端必须更新"))
                Toggle("强制更新", isOn: $mandatory)
                LabeledContent("更新说明") {
                    TextEditor(text: $changelog).font(.body).frame(height: 120)
                        .overlay(RoundedRectangle(cornerRadius: 4).stroke(.separator))
                }
            }
            Text("版本以草稿创建;上传产物后才能发布。").font(.callout).foregroundStyle(.secondary)
            HStack {
                Spacer()
                Button("取消", role: .cancel) { dismiss() }.keyboardShortcut(.cancelAction)
                Button("创建并上传产物", action: submit).keyboardShortcut(.defaultAction)
                    .disabled(busy || version.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(24).frame(width: 520)
    }

    private func submit() {
        guard let client = model.client, let gameID = model.gameID else { return }
        busy = true
        Task {
            defer { busy = false }
            do {
                let release = try await client.createRelease(gameID: gameID, NewRelease(
                    version: version.trimmingCharacters(in: .whitespaces),
                    channel: channel.trimmingCharacters(in: .whitespaces).isEmpty ? "stable" : channel,
                    changelog: changelog, minSupportedVersion: minSupported.trimmingCharacters(in: .whitespaces),
                    mandatory: mandatory))
                model.refresh()
                dismiss()
                created(release)
            } catch {
                model.report(error)
            }
        }
    }
}

private struct ArtifactsSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var release: Release
    @State private var artifacts: [Artifact] = []
    @State private var error: String?
    @State private var platform = "any"
    @State private var arch = "any"
    @State private var picking = false
    @State private var progress: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                VStack(alignment: .leading) {
                    Text("产物 · \(release.version)").font(.title2.weight(.semibold))
                    Text("渠道 \(release.channel) · \(release.status.label)").foregroundStyle(.secondary)
                }
                Spacer()
                Button("完成") { dismiss() }.keyboardShortcut(.defaultAction)
            }
            if let error {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
            }
            Table(artifacts) {
                TableColumn("平台") { Text("\($0.platform)/\($0.arch)") }.width(110)
                TableColumn("文件") { Text($0.filename).lineLimit(1) }
                TableColumn("大小") { Text(formatBytes($0.size)).monospacedDigit() }.width(80)
                TableColumn("SHA-256") { a in
                    Text(a.sha256.prefix(12) + "…").font(.caption.monospaced()).help(a.sha256).textSelection(.enabled)
                }
                .width(110)
                TableColumn("状态") { a in Badge(text: a.isReady ? "就绪" : "待上传", tone: a.isReady ? .ok : .warn) }
                    .width(60)
            }
            .frame(height: 200)
            HStack {
                Picker("平台", selection: $platform) { ForEach(Artifact.platforms, id: \.self) { Text($0) } }
                    .frame(width: 160)
                Picker("架构", selection: $arch) { ForEach(Artifact.archs, id: \.self) { Text($0) } }
                    .frame(width: 140)
                Spacer()
                if let progress {
                    ProgressView().controlSize(.small)
                    Text(progress).foregroundStyle(.secondary)
                }
                Button("选择文件并上传…") { picking = true }
                    .disabled(!model.canWrite || progress != nil)
            }
            Text("同一平台/架构重复上传会覆盖旧产物。").font(.caption).foregroundStyle(.secondary)
        }
        .padding(24).frame(width: 720)
        .task { await load() }
        .fileImporter(isPresented: $picking, allowedContentTypes: [.item]) { result in
            if case .success(let url) = result { upload(url) }
        }
    }

    private func load() async {
        guard let client = model.client, let gameID = model.gameID else { return }
        do {
            artifacts = try await client.artifacts(gameID: gameID, version: release.version, channel: release.channel)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func upload(_ url: URL) {
        guard let client = model.client, let gameID = model.gameID else { return }
        progress = "准备中…"
        Task {
            let scoped = url.startAccessingSecurityScopedResource()
            defer {
                if scoped { url.stopAccessingSecurityScopedResource() }
                progress = nil
            }
            do {
                _ = try await client.uploadArtifact(
                    gameID: gameID, version: release.version, channel: release.channel,
                    platform: platform, arch: arch, file: url
                ) { step in Task { @MainActor in progress = step } }
                model.flash("已上传 \(url.lastPathComponent)")
                await load()
                model.refresh()
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

private struct RolloutSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var release: Release
    @State private var percent: Double = 100

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("灰度 · \(release.version)").font(.title2.weight(.semibold))
            Text("只有按设备哈希落在比例内的客户端会收到这个版本。").foregroundStyle(.secondary)
            HStack {
                Slider(value: $percent, in: 0...100, step: 5)
                Text("\(Int(percent))%").monospacedDigit().frame(width: 48, alignment: .trailing)
            }
            HStack {
                Spacer()
                Button("取消", role: .cancel) { dismiss() }.keyboardShortcut(.cancelAction)
                Button("保存") {
                    guard let gameID = model.gameID else { return }
                    let value = Int(percent)
                    model.perform("灰度已调整为 \(value)%") { [client = model.client] in
                        try await client?.rollout(gameID: gameID, version: release.version,
                                                  channel: release.channel, percent: value)
                    }
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(24).frame(width: 420)
        .onAppear { percent = Double(release.rolloutPercent) }
    }
}
