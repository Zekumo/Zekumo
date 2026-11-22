import Foundation
import Observation
import SwiftUI
import ZekumoKit

enum Page: String, Hashable, CaseIterable {
    case dashboard, games, players, releases, announcements, bans

    var title: String {
        switch self {
        case .dashboard: "仪表盘"
        case .games: "游戏"
        case .players: "玩家"
        case .releases: "版本更新"
        case .announcements: "公告"
        case .bans: "封禁"
        }
    }

    var symbol: String {
        switch self {
        case .dashboard: "square.grid.2x2"
        case .games: "gamecontroller"
        case .players: "person.2"
        case .releases: "shippingbox"
        case .announcements: "megaphone"
        case .bans: "nosign"
        }
    }

    var isGameScoped: Bool { ![.dashboard, .games].contains(self) }
}

/// A destructive or consequential action waiting for the operator to confirm.
struct PendingConfirmation: Identifiable {
    let id = UUID()
    var title: String
    var message: String
    var confirmLabel: String
    var destructive = true
    var action: () async throws -> Void
}

/// Session state shared by every screen: server, identity, the selected
/// workspace and game, and the role that decides which controls are enabled.
@MainActor
@Observable
final class AppModel {
    private let defaults = UserDefaults.standard

    var serverText: String { didSet { defaults.set(serverText, forKey: "serverURL") } }
    var username: String { didSet { defaults.set(username, forKey: "username") } }

    private(set) var client: ZekumoClient?
    private(set) var me: Me?
    private(set) var workspaces: [Workspace] = []
    private(set) var games: [Game] = []
    private(set) var restoring = true

    var workspaceID: String? {
        didSet {
            guard workspaceID != oldValue else { return }
            client?.workspaceID = workspaceID
            defaults.set(workspaceID, forKey: "workspaceID")
            gameID = nil
            Task { await reloadGames() }
        }
    }

    var gameID: String? {
        didSet { if gameID == nil, page?.isGameScoped == true { page = .games } }
    }

    var page: Page? = .dashboard
    /// Bumped to make the visible screen fetch again (⌘R, or after a mutation elsewhere).
    private(set) var reloadToken = 0
    var toast: String?
    var errorMessage: String?
    var confirmation: PendingConfirmation?

    init() {
        serverText = defaults.string(forKey: "serverURL") ?? "http://localhost:8080"
        username = defaults.string(forKey: "username") ?? "admin"
    }

    var isSignedIn: Bool { client != nil && me != nil }
    var workspace: Workspace? { workspaces.first { $0.id == workspaceID } }
    var role: Role { workspace?.role ?? .viewer }
    var canWrite: Bool { role >= .editor }
    var canManage: Bool { role >= .admin }
    var isOwner: Bool { role >= .owner }
    var game: Game? { games.first { $0.id == gameID } }
    var warnings: [String] { me?.warnings ?? [] }

    /// Changes whenever the visible data should be fetched again.
    var loadKey: String { "\(workspaceID ?? "")|\(gameID ?? "")|\(reloadToken)" }

    // MARK: - Session

    func restore() async {
        defer { restoring = false }
        guard let url = ZekumoClient.normalizeServerURL(serverText),
              let token = Keychain.token(for: url.absoluteString) else { return }
        let client = ZekumoClient(baseURL: url, token: token)
        do {
            let me = try await client.me()
            adopt(client: client, me: me, workspaces: me.workspaces,
                  preferred: defaults.string(forKey: "workspaceID") ?? me.workspaces.first?.id)
        } catch ZekumoError.unauthorized {
            Keychain.deleteToken(for: url.absoluteString)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func login(password: String) async throws {
        guard let url = ZekumoClient.normalizeServerURL(serverText) else {
            throw ZekumoError.invalidURL(serverText)
        }
        let client = ZekumoClient(baseURL: url)
        let res = try await client.login(username: username.trimmingCharacters(in: .whitespaces), password: password)
        client.token = res.token
        let me = try await client.me()
        Keychain.setToken(res.token, for: url.absoluteString)
        serverText = url.absoluteString
        let saved = defaults.string(forKey: "workspaceID")
        let preferred = res.workspaces.contains { $0.id == saved } ? saved : res.defaultWorkspaceId
        adopt(client: client, me: me, workspaces: res.workspaces, preferred: preferred)
    }

    private func adopt(client: ZekumoClient, me: Me, workspaces: [Workspace], preferred: String?) {
        self.client = client
        self.me = me
        self.workspaces = workspaces
        let id = workspaces.contains { $0.id == preferred } ? preferred : workspaces.first?.id
        client.workspaceID = id
        if workspaceID == id { Task { await reloadGames() } } else { workspaceID = id }
        page = .dashboard
    }

    func logout() {
        if let client { Keychain.deleteToken(for: client.baseURL.absoluteString) }
        client = nil
        me = nil
        workspaces = []
        games = []
        gameID = nil
        page = .dashboard
    }

    func refresh() {
        reloadToken += 1
        Task { await reloadGames() }
    }

    func reloadGames() async {
        guard let client else { return }
        do {
            games = try await client.games()
            if let gameID, !games.contains(where: { $0.id == gameID }) { self.gameID = nil }
        } catch {
            report(error)
        }
    }

    func open(game: Game, page: Page = .players) {
        gameID = game.id
        self.page = page
    }

    // MARK: - Feedback

    func flash(_ text: String) {
        toast = text
        Task {
            try? await Task.sleep(for: .seconds(3))
            if toast == text { toast = nil }
        }
    }

    func report(_ error: Error) {
        if case ZekumoError.unauthorized = error {
            logout()
            errorMessage = ZekumoError.unauthorized.errorDescription
            return
        }
        if error is CancellationError || (error as? URLError)?.code == .cancelled { return }
        errorMessage = error.localizedDescription
    }

    /// Runs a mutation, reporting failures and refreshing the screen on success.
    func perform(_ success: String? = nil, _ work: @escaping () async throws -> Void) {
        Task {
            do {
                try await work()
                if let success { flash(success) }
                refresh()
            } catch {
                report(error)
            }
        }
    }

    func confirm(_ title: String, message: String = "", label: String, destructive: Bool = true,
                 success: String? = nil, action: @escaping () async throws -> Void) {
        confirmation = PendingConfirmation(title: title, message: message, confirmLabel: label,
                                           destructive: destructive) { [weak self] in
            try await action()
            if let success { self?.flash(success) }
            self?.refresh()
        }
    }
}
