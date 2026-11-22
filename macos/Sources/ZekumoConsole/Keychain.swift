import Foundation
import Security

/// Stores the console session token per server in the login keychain, so it
/// never lands in UserDefaults on disk in plain text.
enum Keychain {
    private static let service = "ai.zekumo.console"

    private static func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: account]
    }

    static func token(for server: String) -> String? {
        var q = query(server)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func setToken(_ token: String, for server: String) {
        deleteToken(for: server)
        var q = query(server)
        q[kSecValueData as String] = Data(token.utf8)
        q[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlocked
        SecItemAdd(q as CFDictionary, nil)
    }

    static func deleteToken(for server: String) {
        SecItemDelete(query(server) as CFDictionary)
    }
}
