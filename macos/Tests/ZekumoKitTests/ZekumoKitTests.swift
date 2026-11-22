import XCTest
@testable import ZekumoKit

final class ZekumoKitTests: XCTestCase {
    func testParsesGoTimestamps() throws {
        let micro = try XCTUnwrap(ZekumoClient.parseDate("2026-11-22T04:03:49.842505-08:00"))
        let plain = try XCTUnwrap(ZekumoClient.parseDate("2026-11-29T04:03:49-08:00"))
        let zulu = try XCTUnwrap(ZekumoClient.parseDate("2026-11-22T12:03:49.1Z"))
        XCTAssertEqual(plain.timeIntervalSince(micro), 7 * 86400 - 0.842505, accuracy: 0.01)
        XCTAssertEqual(zulu.timeIntervalSince(micro), 0.1 - 0.842505, accuracy: 0.01)
    }

    func testDecodesLoginAndGames() throws {
        let login = """
        {"default_workspace_id":"w1","token":"t","user":{"id":"u","kind":"bootstrap","username":"admin","created_at":"2026-11-22T04:03:49.842505-08:00"},
         "workspaces":[{"id":"w1","name":"Legacy workspace","slug":"legacy","organization_id":"o","organization_name":"Org","role":"owner","created_at":"2026-11-22T04:03:37.146726-08:00"}]}
        """
        let r = try ZekumoClient.decode(LoginResponse.self, status: 200, data: Data(login.utf8))
        XCTAssertEqual(r.defaultWorkspaceId, "w1")
        XCTAssertEqual(r.workspaces.first?.role, .owner)

        let games = """
        {"games":[{"id":"g","workspace_id":"w1","app_id":"zk_1","app_secret":"s","name":"云猫冒险","status":"active",
          "created_at":"2026-11-22T04:03:49.967124-08:00","sso_redirect_urls":"","func_http_allowlist":"","friend_limit":200,"online":3}]}
        """
        struct R: Decodable { var games: [Game] }
        let g = try ZekumoClient.decode(R.self, status: 200, data: Data(games.utf8)).games[0]
        XCTAssertEqual(g.appId, "zk_1")
        XCTAssertEqual(g.online, 3)
    }

    func testDecodesStatsHealthAndBans() throws {
        let stats = """
        {"totals":{"games":2,"players":5,"accounts":0,"online":0},"today":{"date":"2026-11-22","active":0,"logins":5,"new_players":5},
         "days":[{"date":"2026-11-22","active":0,"logins":5,"new_players":5}],
         "games":[{"id":"g","name":"x","online":0,"active":0,"logins":5,"new_players":5,"trend":[0,1]}],
         "retention":{"d1_cohort":1,"d1_back":0,"d7_cohort":0,"d7_back":0},"timezone":"Asia/Shanghai"}
        """
        let s = try ZekumoClient.decode(PlatformStats.self, status: 200, data: Data(stats.utf8))
        XCTAssertEqual(s.today.newPlayers, 5)
        XCTAssertEqual(s.retention.d1Cohort, 1)

        let health = """
        {"version":"dev","env":"development","uptime_seconds":13,"postgres":{"ok":true,"latency_ms":0.101},
         "redis":{"ok":false,"latency_ms":0,"error":"down"},"artifact_bytes":0,"log_rows":1,"chat_rows":0,
         "webhook_total":0,"webhook_ok":0,"function_errors_24h":2,"http_errors_24h":0}
        """
        let h = try ZekumoClient.decode(Health.self, status: 200, data: Data(health.utf8))
        XCTAssertEqual(h.functionErrors24h, 2)
        XCTAssertEqual(h.redis.error, "down")

        let ban = """
        {"id":"b","game_id":"g","player_id":"p","reason":"外挂","operator":"op","expires_at":"2000-01-01T00:00:00Z",
         "created_at":"1999-12-31T00:00:00Z","nickname":"云朵"}
        """
        let b = try ZekumoClient.decode(PlayerBan.self, status: 200, data: Data(ban.utf8))
        XCTAssertEqual(b.operator_, "op")
        XCTAssertFalse(b.isActive, "an expired ban is no longer in force")
    }

    func testSaveValuesKeepTheirShape() throws {
        let body = #"{"entries":[{"key":"slot1","value":{"gold":120,"items":["sword"],"hp":0.5},"updated_at":"2026-11-22T04:03:50Z"}]}"#
        struct R: Decodable { var entries: [SaveEntry] }
        let e = try ZekumoClient.decode(R.self, status: 200, data: Data(body.utf8)).entries[0]
        XCTAssertEqual(e.value?.prettyPrinted, """
        {
          "gold" : 120,
          "hp" : 0.5,
          "items" : [
            "sword"
          ]
        }
        """)
    }

    func testLargeIntegersSurviveSaveInspection() throws {
        let v = try JSONDecoder().decode(JSONValue.self, from: Data("[9007199254740993, 2.5e20]".utf8))
        XCTAssertEqual(v.prettyPrinted.filter { !$0.isWhitespace }, "[9007199254740993,2.5e+20]")
    }

    func testStatsAcceptNullGames() throws {
        let body = #"{"totals":{"games":0,"players":0,"accounts":0,"online":0},"today":{"date":"2026-11-22","active":0,"logins":0,"new_players":0},"days":[],"games":null,"retention":{"d1_cohort":0,"d1_back":0,"d7_cohort":0,"d7_back":0},"timezone":"UTC"}"#
        let s = try ZekumoClient.decode(PlatformStats.self, status: 200, data: Data(body.utf8))
        XCTAssertTrue(s.games.isEmpty)
    }

    func testErrorsMapToMessages() {
        let body = Data(#"{"error":{"code":"already_banned","message":"player already has an active ban"}}"#.utf8)
        XCTAssertThrowsError(try ZekumoClient.decode(PlayerBan.self, status: 409, data: body)) { error in
            XCTAssertEqual(error as? ZekumoError,
                           .server(status: 409, code: "already_banned", message: "player already has an active ban"))
        }
        XCTAssertThrowsError(try ZekumoClient.decode(Me.self, status: 401, data: Data())) { error in
            XCTAssertEqual(error as? ZekumoError, .unauthorized)
        }
        // A failed login is a credential error, not an expired session.
        XCTAssertThrowsError(try ZekumoClient.decode(Me.self, status: 401, data: body, isLogin: true)) { error in
            XCTAssertNotEqual(error as? ZekumoError, .unauthorized)
        }
    }

    func testRequestsCarryAuthAndWorkspace() throws {
        let client = ZekumoClient(baseURL: URL(string: "http://localhost:8090")!, token: "tok", workspaceID: "w1")
        let req = try client.makeRequest("POST", "/admin/api/games/g/releases/\(ZekumoClient.segment("1.0.0+b/1"))/publish",
                                         query: ["channel": "beta", "empty": ""])
        XCTAssertEqual(req.url?.absoluteString,
                       "http://localhost:8090/admin/api/games/g/releases/1.0.0%2Bb%2F1/publish?channel=beta")
        XCTAssertEqual(req.value(forHTTPHeaderField: "Authorization"), "Bearer tok")
        XCTAssertEqual(req.value(forHTTPHeaderField: ZekumoClient.workspaceHeader), "w1")
    }

    func testNormalizesServerURL() {
        XCTAssertEqual(ZekumoClient.normalizeServerURL(" localhost:8080/ ")?.absoluteString, "http://localhost:8080")
        XCTAssertEqual(ZekumoClient.normalizeServerURL("https://api.example.com")?.absoluteString, "https://api.example.com")
        XCTAssertNil(ZekumoClient.normalizeServerURL("ftp://x"))
        XCTAssertNil(ZekumoClient.normalizeServerURL(""))
    }

    func testDigestStreamsFile() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try Data("abc".utf8).write(to: url)
        defer { try? FileManager.default.removeItem(at: url) }
        let (size, sha) = try ZekumoClient.digest(of: url)
        XCTAssertEqual(size, 3)
        XCTAssertEqual(sha, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
    }

    func testRolesAreOrdered() {
        XCTAssertTrue(Role.viewer < .editor)
        XCTAssertTrue(Role.owner >= .admin)
    }
}
