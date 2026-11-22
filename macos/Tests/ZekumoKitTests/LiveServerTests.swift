import XCTest
@testable import ZekumoKit

/// End-to-end run of every console call against a real server. Skipped unless
/// ZEKUMO_LIVE_URL is set, e.g.
///   ZEKUMO_LIVE_URL=http://localhost:8080 ZEKUMO_LIVE_PASSWORD=admin123 swift test
final class LiveServerTests: XCTestCase {
    func testConsoleRoundTrip() async throws {
        let env = ProcessInfo.processInfo.environment
        guard let raw = env["ZEKUMO_LIVE_URL"], let url = ZekumoClient.normalizeServerURL(raw) else {
            throw XCTSkip("ZEKUMO_LIVE_URL not set")
        }
        let client = ZekumoClient(baseURL: url)
        let login = try await client.login(username: env["ZEKUMO_LIVE_USER"] ?? "admin",
                                           password: env["ZEKUMO_LIVE_PASSWORD"] ?? "admin123")
        client.token = login.token
        client.workspaceID = login.defaultWorkspaceId

        let me = try await client.me()
        XCTAssertFalse(me.workspaces.isEmpty)
        _ = try await client.platformStats(days: 7)
        _ = try await client.platformStats(days: 30)
        _ = try await client.logs(level: "error", limit: 6)
        if me.activeRole == .owner { _ = try await client.health() }

        let game = try await client.createGame(name: "macOS live test \(UUID().uuidString.prefix(6))")
        addTeardownBlock { try? await client.deleteGame(id: game.id) }
        let games = try await client.games()
        XCTAssertTrue(games.contains { $0.id == game.id })
        let players = try await client.players(gameID: game.id)
        XCTAssertEqual(players, [])
        let bans = try await client.bans(gameID: game.id)
        XCTAssertEqual(bans, [])

        // Release lifecycle: draft → artifact → publish → rollout → revoke; only drafts can be deleted.
        let version = "1.0.\(Int.random(in: 0...9999))"
        let release = try await client.createRelease(gameID: game.id, NewRelease(
            version: version, channel: "beta", changelog: "- 测试", minSupportedVersion: "", mandatory: true))
        XCTAssertEqual(release.status, .draft)
        let file = FileManager.default.temporaryDirectory.appendingPathComponent("zekumo-\(version).zip")
        try Data(repeating: 7, count: 300_000).write(to: file)
        defer { try? FileManager.default.removeItem(at: file) }
        let artifact = try await client.uploadArtifact(gameID: game.id, version: version, channel: "beta",
                                                       platform: "macos", arch: "arm64", file: file)
        XCTAssertTrue(artifact.isReady)
        let artifacts = try await client.artifacts(gameID: game.id, version: version, channel: "beta")
        XCTAssertEqual(artifacts.count, 1)
        try await client.publishRelease(gameID: game.id, version: version, channel: "beta")
        try await client.rollout(gameID: game.id, version: version, channel: "beta", percent: 25)
        var listed = try await client.releases(gameID: game.id)
        XCTAssertEqual(listed.first?.status, .published)
        XCTAssertEqual(listed.first?.rolloutPercent, 25)
        try await client.revokeRelease(gameID: game.id, version: version, channel: "beta")
        listed = try await client.releases(gameID: game.id)
        XCTAssertEqual(listed.first?.status, .revoked)
        let draft = try await client.createRelease(gameID: game.id, NewRelease(
            version: version + "-rc", channel: "beta", changelog: "", minSupportedVersion: "", mandatory: false))
        try await client.deleteRelease(gameID: game.id, version: draft.version, channel: "beta")
        listed = try await client.releases(gameID: game.id)
        XCTAssertEqual(listed.map(\.version), [version])

        // Announcements: create with expiry → edit clearing it → deactivate.
        try await client.createAnnouncement(gameID: game.id, AnnouncementInput(
            title: "维护通知", body: "**今晚** 维护", importance: .warning, platform: "", channel: "",
            expiresInSecs: 3600))
        var notices = try await client.announcements(gameID: game.id)
        var notice = try XCTUnwrap(notices.first)
        XCTAssertNotNil(notice.expiresAt)
        try await client.updateAnnouncement(id: notice.id, AnnouncementInput(
            title: "维护完成", body: notice.body, importance: .info, platform: "", channel: "", clearExpires: true))
        notices = try await client.announcements(gameID: game.id)
        notice = try XCTUnwrap(notices.first)
        XCTAssertEqual(notice.title, "维护完成")
        XCTAssertNil(notice.expiresAt)
        try await client.deactivateAnnouncement(id: notice.id)
        notices = try await client.announcements(gameID: game.id)
        XCTAssertEqual(notices.first?.active, false)
    }
}
