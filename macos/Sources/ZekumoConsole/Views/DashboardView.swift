import Charts
import SwiftUI
import ZekumoKit

struct DashboardView: View {
    @Environment(AppModel.self) private var model
    @State private var stats: PlatformStats?
    @State private var health: Health?
    @State private var errors: [LogEntry] = []
    @State private var loading = true
    @State private var error: String?
    @State private var days = 7
    @State private var metric = Metric.active

    enum Metric: String, CaseIterable, Identifiable {
        case active, logins, newPlayers
        var id: Self { self }
        var label: String {
            switch self {
            case .active: "活跃"
            case .logins: "登录"
            case .newPlayers: "新增"
            }
        }
        func value(_ row: DayRow) -> Int {
            switch self {
            case .active: row.active
            case .logins: row.logins
            case .newPlayers: row.newPlayers
            }
        }
    }

    var body: some View {
        LoadState(loading: loading && stats == nil, error: error, retry: { Task { await load() } }) {
            if let stats {
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        SecurityBanner(warnings: model.warnings)
                        tiles(stats)
                        trend(stats)
                        HStack(alignment: .top, spacing: 16) {
                            perGame(stats)
                            VStack(spacing: 16) {
                                if let health { healthCard(health) }
                                recentErrors
                            }
                            .frame(width: 320)
                        }
                    }
                    .padding(20)
                }
            }
        }
        .task(id: "\(model.loadKey)|\(days)") { await load() }
    }

    private func load() async {
        guard let client = model.client else { return }
        loading = true
        defer { loading = false }
        do {
            async let s = client.platformStats(days: days)
            async let e = client.logs(level: "error", limit: 6)
            let h: Health? = model.isOwner ? try await client.health() : nil
            (stats, errors, health) = try await (s, e, h)
            error = nil
        } catch is CancellationError {
        } catch {
            if case ZekumoError.unauthorized = error { model.report(error) }
            self.error = error.localizedDescription
        }
    }

    private func tiles(_ s: PlatformStats) -> some View {
        Grid(horizontalSpacing: 12, verticalSpacing: 12) {
            GridRow {
                Tile(title: "当前在线", value: "\(s.totals.online)", detail: "\(s.totals.games) 个游戏")
                Tile(title: "今日活跃", value: "\(s.today.active)", detail: "\(s.today.logins) 次登录")
                Tile(title: "今日新增", value: "\(s.today.newPlayers)", detail: "累计 \(s.totals.players) 名玩家")
                Tile(title: "次日留存", value: percent(s.retention.d1Back, s.retention.d1Cohort),
                     detail: "7 日 \(percent(s.retention.d7Back, s.retention.d7Cohort))")
            }
        }
    }

    private func trend(_ s: PlatformStats) -> some View {
        Card("趋势", trailing: AnyView(HStack {
            Picker("指标", selection: $metric) {
                ForEach(Metric.allCases) { Text($0.label).tag($0) }
            }
            .pickerStyle(.segmented).frame(width: 180)
            Picker("范围", selection: $days) {
                Text("7 天").tag(7)
                Text("30 天").tag(30)
            }
            .pickerStyle(.segmented).frame(width: 120)
        }.labelsHidden())) {
            Chart(s.days, id: \.date) { row in
                BarMark(x: .value("日期", String(row.date.suffix(5))), y: .value(metric.label, metric.value(row)))
                    .foregroundStyle(Color.accentColor.gradient)
                    .cornerRadius(3)
            }
            .frame(height: 180)
            Text("时区 \(s.timezone)").font(.caption).foregroundStyle(.secondary)
        }
    }

    private func perGame(_ s: PlatformStats) -> some View {
        Card("各游戏今日") {
            if s.games.isEmpty {
                MascotState(mascot: .empty, title: "还没有游戏", hint: "在「游戏」里创建第一个游戏。")
                    .frame(height: 260)
            } else {
                Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 10) {
                    GridRow {
                        Text("游戏"); Text("在线"); Text("活跃"); Text("新增"); Text("近期")
                    }
                    .font(.caption).foregroundStyle(.secondary)
                    Divider()
                    ForEach(s.games) { g in
                        GridRow {
                            Button(g.name) {
                                if let game = model.games.first(where: { $0.id == g.id }) { model.open(game: game) }
                            }
                            .buttonStyle(.link)
                            Text("\(g.online)").monospacedDigit()
                            Text("\(g.active)").monospacedDigit()
                            Text("\(g.newPlayers)").monospacedDigit()
                            Chart(Array(g.trend.enumerated()), id: \.offset) { item in
                                LineMark(x: .value("d", item.offset), y: .value("v", item.element))
                                    .interpolationMethod(.monotone)
                            }
                            .chartXAxis(.hidden).chartYAxis(.hidden)
                            .frame(width: 90, height: 22)
                        }
                    }
                }
            }
        }
    }

    private func healthCard(_ h: Health) -> some View {
        Card("服务状态", trailing: AnyView(Text("\(h.version) · \(h.env)").font(.caption).foregroundStyle(.secondary))) {
            VStack(alignment: .leading, spacing: 8) {
                dependency("PostgreSQL", h.postgres)
                dependency("Redis", h.redis)
                LabeledContent("运行时间", value: Duration.seconds(h.uptimeSeconds)
                    .formatted(.units(allowed: [.days, .hours, .minutes], width: .abbreviated)))
                LabeledContent("产物存储", value: formatBytes(h.artifactBytes))
                LabeledContent("24 小时错误", value: "函数 \(h.functionErrors24h) · HTTP \(h.httpErrors24h)")
            }
            .font(.callout)
        }
    }

    private func dependency(_ name: String, _ dep: Dependency) -> some View {
        LabeledContent(name) {
            if dep.ok {
                Badge(text: String(format: "正常 %.1f ms", dep.latencyMs), tone: .ok)
            } else {
                Badge(text: dep.error ?? "异常", tone: .error)
            }
        }
    }

    private var recentErrors: some View {
        Card("最近错误") {
            if errors.isEmpty {
                Label("暂无错误", systemImage: "checkmark.circle").foregroundStyle(.secondary).font(.callout)
            } else {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(errors) { log in
                        VStack(alignment: .leading, spacing: 2) {
                            Text(log.message).font(.callout).lineLimit(2)
                            Text("\(log.source) · \(log.createdAt.ago)").font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
    }
}
