import CryptoKit
import Foundation

public enum ZekumoError: LocalizedError, Equatable {
    case unauthorized
    case server(status: Int, code: String, message: String)
    case invalidURL(String)
    case invalidResponse

    public var errorDescription: String? {
        switch self {
        case .unauthorized: "登录已过期,请重新登录"
        case .server(let status, let code, let message):
            message.isEmpty ? "HTTP \(status) (\(code))" : message
        case .invalidURL(let url): "无效的地址:\(url)"
        case .invalidResponse: "服务器返回了无法识别的响应"
        }
    }
}

/// Async client for the Zekumo developer console API (`/admin/api`).
///
/// Every request after login carries the bearer token and, once a workspace
/// is selected, the `X-Zekumo-Workspace-ID` header the server scopes data by.
public final class ZekumoClient: @unchecked Sendable {
    public static let workspaceHeader = "X-Zekumo-Workspace-ID"

    public let baseURL: URL
    public var token: String?
    public var workspaceID: String?
    private let session: URLSession

    public init(baseURL: URL, token: String? = nil, workspaceID: String? = nil, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.token = token
        self.workspaceID = workspaceID
        self.session = session
    }

    /// Parses user input like `localhost:8080` or `https://api.example.com/`.
    public static func normalizeServerURL(_ raw: String) -> URL? {
        var text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        while text.hasSuffix("/") { text.removeLast() }
        if text.isEmpty { return nil }
        if !text.contains("://") { text = "http://" + text }
        guard let url = URL(string: text), let scheme = url.scheme?.lowercased(),
              ["http", "https"].contains(scheme), url.host != nil else { return nil }
        return url
    }

    // MARK: - Encoding

    public static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        d.dateDecodingStrategy = .custom { decoder in
            let c = try decoder.singleValueContainer()
            let text = try c.decode(String.self)
            if let date = parseDate(text) { return date }
            throw DecodingError.dataCorruptedError(in: c, debugDescription: "invalid date \(text)")
        }
        return d
    }()

    static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        return e
    }()

    /// Go's time.Time marshals as RFC 3339 with up to nine fractional digits.
    public static func parseDate(_ text: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = fractional.date(from: text) { return d }
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        return plain.date(from: text)
    }

    /// Percent-encodes one path segment (a version like `1.0.0+build` or an ID).
    public static func segment(_ value: String) -> String {
        var allowed = CharacterSet.urlPathAllowed
        allowed.remove(charactersIn: "/?#+")
        return value.addingPercentEncoding(withAllowedCharacters: allowed) ?? value
    }

    // MARK: - Transport

    private struct Empty: Codable {}
    private struct ErrorBody: Decodable {
        struct Inner: Decodable { var code: String; var message: String }
        var error: Inner
    }

    func makeRequest(_ method: String, _ path: String, query: [String: String] = [:], body: Data? = nil) throws -> URLRequest {
        let raw = baseURL.absoluteString + path
        guard var components = URLComponents(string: raw) else { throw ZekumoError.invalidURL(raw) }
        let items = query.filter { !$0.value.isEmpty }.sorted { $0.key < $1.key }
            .map { URLQueryItem(name: $0.key, value: $0.value) }
        if !items.isEmpty { components.queryItems = (components.queryItems ?? []) + items }
        guard let url = components.url else { throw ZekumoError.invalidURL(raw) }
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.timeoutInterval = 30
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body {
            req.httpBody = body
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        if let workspaceID { req.setValue(workspaceID, forHTTPHeaderField: Self.workspaceHeader) }
        return req
    }

    func send<T: Decodable>(_ method: String, _ path: String, query: [String: String] = [:],
                            body: (some Encodable)? = Optional<Empty>.none, as: T.Type = T.self) async throws -> T {
        let data = try body.map { try Self.encoder.encode($0) }
        let req = try makeRequest(method, path, query: query, body: data ?? (method == "GET" || method == "DELETE" ? nil : Data("{}".utf8)))
        let (respData, resp) = try await session.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw ZekumoError.invalidResponse }
        return try Self.decode(T.self, status: http.statusCode, data: respData, isLogin: path == "/admin/api/login")
    }

    static func decode<T: Decodable>(_ type: T.Type, status: Int, data: Data, isLogin: Bool = false) throws -> T {
        guard (200..<300).contains(status) else {
            let body = try? decoder.decode(ErrorBody.self, from: data)
            if status == 401 && !isLogin { throw ZekumoError.unauthorized }
            throw ZekumoError.server(status: status, code: body?.error.code ?? "http_\(status)",
                                     message: body?.error.message ?? HTTPURLResponse.localizedString(forStatusCode: status))
        }
        let payload = data.isEmpty ? Data("{}".utf8) : data
        return try decoder.decode(T.self, from: payload)
    }

    private func discard(_ method: String, _ path: String, query: [String: String] = [:], body: (some Encodable)? = Optional<Empty>.none) async throws {
        let _: Empty = try await send(method, path, query: query, body: body)
    }

    private func game(_ id: String) -> String { "/admin/api/games/\(Self.segment(id))" }

    // MARK: - Identity and workspaces

    public func login(username: String, password: String) async throws -> LoginResponse {
        struct Body: Encodable { var username: String; var password: String }
        return try await send("POST", "/admin/api/login", body: Body(username: username, password: password))
    }

    public func me() async throws -> Me { try await send("GET", "/admin/api/me") }

    // MARK: - Dashboard

    public func platformStats(days: Int) async throws -> PlatformStats {
        try await send("GET", "/admin/api/stats", query: ["days": String(days)])
    }

    public func health() async throws -> Health { try await send("GET", "/admin/api/health") }

    public func logs(level: String = "", gameID: String = "", limit: Int = 50) async throws -> [LogEntry] {
        struct R: Decodable { var entries: [LogEntry] }
        let r: R = try await send("GET", "/admin/api/logs", query: ["level": level, "game_id": gameID, "limit": String(limit)])
        return r.entries
    }

    // MARK: - Games

    public func games() async throws -> [Game] {
        struct R: Decodable { var games: [Game] }
        let r: R = try await send("GET", "/admin/api/games")
        return r.games
    }

    public func createGame(name: String) async throws -> Game {
        struct Body: Encodable { var name: String }
        return try await send("POST", "/admin/api/games", body: Body(name: name))
    }

    public func deleteGame(id: String) async throws { try await discard("DELETE", game(id)) }

    // MARK: - Players and bans

    public func players(gameID: String, search: String = "", limit: Int = 200) async throws -> [Player] {
        struct R: Decodable { var players: [Player] }
        let r: R = try await send("GET", game(gameID) + "/players", query: ["search": search, "limit": String(limit)])
        return r.players
    }

    public func playerData(playerID: String) async throws -> [SaveEntry] {
        struct R: Decodable { var entries: [SaveEntry] }
        let r: R = try await send("GET", "/admin/api/players/\(Self.segment(playerID))/data")
        return r.entries
    }

    public func ban(playerID: String, reason: String, durationSecs: Int64) async throws {
        struct Body: Encodable { var reason: String; var durationSecs: Int64 }
        try await discard("POST", "/admin/api/players/\(Self.segment(playerID))/ban",
                          body: Body(reason: reason, durationSecs: durationSecs))
    }

    public func unban(playerID: String) async throws {
        try await discard("POST", "/admin/api/players/\(Self.segment(playerID))/unban")
    }

    public func bans(gameID: String) async throws -> [PlayerBan] {
        struct R: Decodable { var bans: [PlayerBan]? }
        let r: R = try await send("GET", game(gameID) + "/bans")
        return r.bans ?? []
    }

    // MARK: - Releases

    private func release(_ gameID: String, _ version: String) -> String {
        game(gameID) + "/releases/" + Self.segment(version)
    }

    public func releases(gameID: String) async throws -> [Release] {
        struct R: Decodable { var releases: [Release] }
        let r: R = try await send("GET", game(gameID) + "/releases")
        return r.releases
    }

    public func createRelease(gameID: String, _ input: NewRelease) async throws -> Release {
        try await send("POST", game(gameID) + "/releases", body: input)
    }

    public func deleteRelease(gameID: String, version: String, channel: String) async throws {
        try await discard("DELETE", release(gameID, version), query: ["channel": channel])
    }

    public func publishRelease(gameID: String, version: String, channel: String) async throws {
        try await discard("POST", release(gameID, version) + "/publish", query: ["channel": channel])
    }

    public func revokeRelease(gameID: String, version: String, channel: String) async throws {
        try await discard("POST", release(gameID, version) + "/revoke", query: ["channel": channel])
    }

    public func rollout(gameID: String, version: String, channel: String, percent: Int) async throws {
        struct Body: Encodable { var percent: Int }
        try await discard("POST", release(gameID, version) + "/rollout", query: ["channel": channel], body: Body(percent: percent))
    }

    public func artifacts(gameID: String, version: String, channel: String) async throws -> [Artifact] {
        struct R: Decodable { var artifacts: [Artifact] }
        let r: R = try await send("GET", release(gameID, version) + "/artifacts", query: ["channel": channel])
        return r.artifacts
    }

    /// Registers, uploads and completes one artifact: the same three steps the
    /// web console runs, with the file streamed from disk rather than held in memory.
    public func uploadArtifact(gameID: String, version: String, channel: String, platform: String, arch: String,
                               file: URL, progress: @Sendable (String) -> Void = { _ in }) async throws -> Artifact {
        progress("计算校验和…")
        let (size, sha256) = try Self.digest(of: file)
        struct Body: Encodable { var platform, arch, filename: String; var size: Int64; var sha256: String }
        progress("登记产物…")
        let reg: ArtifactRegistration = try await send(
            "POST", release(gameID, version) + "/artifacts", query: ["channel": channel],
            body: Body(platform: platform, arch: arch, filename: file.lastPathComponent, size: size, sha256: sha256))

        progress("上传中…")
        guard let uploadURL = URL(string: reg.uploadUrl, relativeTo: baseURL)?.absoluteURL else {
            throw ZekumoError.invalidURL(reg.uploadUrl)
        }
        var put = URLRequest(url: uploadURL)
        put.httpMethod = "PUT"
        put.timeoutInterval = 3600
        let (_, resp) = try await session.upload(for: put, fromFile: file)
        guard let http = resp as? HTTPURLResponse else { throw ZekumoError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            throw ZekumoError.server(status: http.statusCode, code: "upload_failed", message: "上传失败:HTTP \(http.statusCode)")
        }

        progress("确认上传…")
        return try await send("POST", release(gameID, version) + "/artifacts/\(reg.artifact.id)/complete",
                              query: ["channel": channel])
    }

    static func digest(of file: URL) throws -> (Int64, String) {
        let handle = try FileHandle(forReadingFrom: file)
        defer { try? handle.close() }
        var hasher = SHA256()
        var size: Int64 = 0
        while let chunk = try handle.read(upToCount: 1 << 20), !chunk.isEmpty {
            hasher.update(data: chunk)
            size += Int64(chunk.count)
        }
        return (size, hasher.finalize().map { String(format: "%02x", $0) }.joined())
    }

    // MARK: - Announcements

    public func announcements(gameID: String) async throws -> [Announcement] {
        struct R: Decodable { var announcements: [Announcement]? }
        let r: R = try await send("GET", game(gameID) + "/announcements")
        return r.announcements ?? []
    }

    public func createAnnouncement(gameID: String, _ input: AnnouncementInput) async throws {
        try await discard("POST", game(gameID) + "/announcements", body: input)
    }

    public func updateAnnouncement(id: String, _ input: AnnouncementInput) async throws {
        try await discard("PUT", "/admin/api/announcements/\(Self.segment(id))", body: input)
    }

    public func deactivateAnnouncement(id: String) async throws {
        try await discard("DELETE", "/admin/api/announcements/\(Self.segment(id))")
    }
}
