import Foundation

// Wire models for the /admin/api endpoints. Field names follow the Go structs;
// the client decodes with convertFromSnakeCase, so `app_id` becomes `appId`.

public enum Role: String, Codable, Comparable, Sendable, CaseIterable {
    case viewer, editor, admin, owner

    var rank: Int {
        switch self {
        case .viewer: 10
        case .editor: 20
        case .admin: 30
        case .owner: 40
        }
    }

    public static func < (lhs: Role, rhs: Role) -> Bool { lhs.rank < rhs.rank }

    public var label: String {
        switch self {
        case .viewer: "查看者"
        case .editor: "编辑者"
        case .admin: "管理员"
        case .owner: "所有者"
        }
    }
}

public struct Identity: Codable, Hashable, Sendable {
    public var id: String
    public var kind: String
    public var username: String
    public var accountId: String?
}

public struct Workspace: Codable, Identifiable, Hashable, Sendable {
    public var id: String
    public var name: String
    public var slug: String
    public var organizationId: String
    public var organizationName: String
    public var role: Role
    public var createdAt: Date?
}

public struct LoginResponse: Codable, Sendable {
    public var token: String
    public var user: Identity
    public var workspaces: [Workspace]
    public var defaultWorkspaceId: String
}

public struct Me: Codable, Sendable {
    public var username: String
    public var user: Identity
    public var workspaces: [Workspace]
    public var activeWorkspaceId: String?
    public var activeRole: Role?
    public var expiresAt: Date?
    public var warnings: [String]
}

public struct Game: Codable, Identifiable, Hashable, Sendable {
    public var id: String
    public var workspaceId: String
    public var appId: String
    /// Redacted by the server for roles that may not see it.
    public var appSecret: String?
    public var name: String
    public var status: String
    public var createdAt: Date
    public var friendLimit: Int?
    public var online: Int?
}

public struct Player: Codable, Identifiable, Hashable, Sendable {
    public var id: String
    public var gameId: String
    public var provider: String
    public var identifier: String
    public var nickname: String
    public var banned: Bool
    public var accountId: String?
    public var createdAt: Date
    public var lastLoginAt: Date

    public var providerLabel: String {
        switch provider {
        case "guest": "游客"
        case "password": "账密"
        case "sso": "通行证"
        default: provider
        }
    }
}

public struct SaveEntry: Codable, Identifiable, Hashable, Sendable {
    public var key: String
    public var value: JSONValue?
    public var updatedAt: Date
    public var id: String { key }
}

public enum ReleaseStatus: String, Codable, Sendable {
    case draft, published, deprecated, revoked

    public var label: String {
        switch self {
        case .draft: "草稿"
        case .published: "已发布"
        case .deprecated: "已弃用"
        case .revoked: "已下架"
        }
    }
}

public struct Release: Codable, Identifiable, Hashable, Sendable {
    public var id: Int64
    public var gameId: String
    public var channel: String
    public var version: String
    public var changelog: String
    public var status: ReleaseStatus
    public var mandatory: Bool
    public var minSupportedVersion: String?
    public var rolloutPercent: Int
    public var publishedAt: Date?
    public var createdAt: Date
}

public struct NewRelease: Encodable, Sendable {
    public var version: String
    public var channel: String
    public var changelog: String
    public var minSupportedVersion: String
    public var mandatory: Bool

    public init(version: String, channel: String, changelog: String, minSupportedVersion: String, mandatory: Bool) {
        self.version = version
        self.channel = channel
        self.changelog = changelog
        self.minSupportedVersion = minSupportedVersion
        self.mandatory = mandatory
    }
}

public struct Artifact: Codable, Identifiable, Hashable, Sendable {
    public var id: Int64
    public var releaseId: Int64
    public var platform: String
    public var arch: String
    public var filename: String
    public var size: Int64
    public var sha256: String
    public var status: String
    public var createdAt: Date

    public var isReady: Bool { status == "ready" }

    public static let platforms = ["any", "windows", "macos", "linux", "android", "ios", "web"]
    public static let archs = ["any", "amd64", "arm64"]
}

public struct ArtifactRegistration: Codable, Sendable {
    public var artifact: Artifact
    public var uploadUrl: String
}

public enum Importance: String, Codable, CaseIterable, Sendable {
    case info, warning, critical

    public var label: String {
        switch self {
        case .info: "通知"
        case .warning: "提醒"
        case .critical: "重要"
        }
    }
}

public struct Announcement: Codable, Identifiable, Hashable, Sendable {
    public var id: String
    public var gameId: String
    public var title: String
    public var body: String
    public var importance: Importance
    public var platform: String
    public var channel: String
    public var active: Bool
    public var expiresAt: Date?
    public var createdAt: Date
    public var updatedAt: Date

    public var isExpired: Bool { expiresAt.map { $0 <= Date() } ?? false }

    public var stateLabel: String {
        if !active { return "已下架" }
        return isExpired ? "已过期" : "发布中"
    }

    public var targetLabel: String {
        let parts = [platform, channel].filter { !$0.isEmpty }
        return parts.isEmpty ? "全服" : parts.joined(separator: " / ")
    }
}

/// Body for creating or updating an announcement. Nil fields are omitted, so
/// an update without an expiry keeps the existing one.
public struct AnnouncementInput: Encodable, Sendable {
    public var title: String
    public var body: String
    public var importance: Importance
    public var platform: String
    public var channel: String
    public var expiresInSecs: Int?
    public var clearExpires: Bool?

    public init(title: String, body: String, importance: Importance, platform: String, channel: String,
                expiresInSecs: Int? = nil, clearExpires: Bool? = nil) {
        self.title = title
        self.body = body
        self.importance = importance
        self.platform = platform
        self.channel = channel
        self.expiresInSecs = expiresInSecs
        self.clearExpires = clearExpires
    }
}

public struct PlayerBan: Codable, Identifiable, Hashable, Sendable {
    public var id: String
    public var gameId: String
    public var playerId: String
    public var reason: String
    public var operator_: String?
    public var expiresAt: Date?
    public var createdAt: Date
    public var liftedAt: Date?
    public var liftedBy: String?
    public var nickname: String?

    enum CodingKeys: String, CodingKey {
        case id, gameId, playerId, reason, expiresAt, createdAt, liftedAt, liftedBy, nickname
        case operator_ = "operator"
    }

    /// A ban is lifted manually, expired on its own, or still in force.
    public var isActive: Bool {
        if liftedAt != nil { return false }
        if let expiresAt, expiresAt <= Date() { return false }
        return true
    }
}

public struct DayRow: Codable, Hashable, Sendable {
    public var date: String
    public var active: Int
    public var logins: Int
    public var newPlayers: Int
}

public struct GameStat: Codable, Identifiable, Hashable, Sendable {
    public var id: String
    public var name: String
    public var online: Int
    public var active: Int
    public var logins: Int
    public var newPlayers: Int
    public var trend: [Int]
}

public struct Retention: Codable, Hashable, Sendable {
    public var d1Cohort: Int
    public var d1Back: Int
    public var d7Cohort: Int
    public var d7Back: Int
}

public struct PlatformStats: Codable, Sendable {
    public struct Totals: Codable, Sendable {
        public var games: Int
        public var players: Int
        public var accounts: Int
        public var online: Int
    }

    public var totals: Totals
    public var today: DayRow
    public var days: [DayRow]
    public var games: [GameStat]
    public var retention: Retention
    public var timezone: String
}

public struct Dependency: Codable, Hashable, Sendable {
    public var ok: Bool
    public var latencyMs: Double
    public var error: String?
}

public struct Health: Codable, Sendable {
    public var version: String
    public var env: String
    public var uptimeSeconds: Int64
    public var postgres: Dependency
    public var redis: Dependency
    public var artifactBytes: Int64
    public var logRows: Int64
    public var chatRows: Int64
    public var webhookTotal: Int64
    public var webhookOk: Int64
    public var functionErrors24h: Int64
    public var httpErrors24h: Int64

    // convertFromSnakeCase capitalizes "24h" to "24H", so name those keys explicitly.
    enum CodingKeys: String, CodingKey {
        case version, env, uptimeSeconds, postgres, redis, artifactBytes, logRows, chatRows, webhookTotal, webhookOk
        case functionErrors24h = "functionErrors24H"
        case httpErrors24h = "httpErrors24H"
    }
}

public struct LogEntry: Codable, Identifiable, Hashable, Sendable {
    public var id: Int64
    public var gameId: String?
    public var level: String
    public var source: String
    public var event: String?
    public var message: String
    public var createdAt: Date
}
