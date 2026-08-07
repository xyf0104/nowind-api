import Foundation
import Security

enum SecureStore {
    private static let service = "com.xiass.admin"
    private static let credentialsAccount = "session.credentials"
    private static let baseURLKey = "xiass.admin.base-url"
    private static let activeSMSSessionAccount = "xiass.sms.receiver.active-session-id"
    private static let legacySMSCardKeyAccounts = [
        "sms.pixlab.card-key-queue",
        "sms.pixlab.active-card-key"
    ]

    static var baseURL: String? {
        get { UserDefaults.standard.string(forKey: baseURLKey) }
        set { UserDefaults.standard.set(newValue, forKey: baseURLKey) }
    }

    static func save(_ credentials: SessionCredentials) throws {
        let data = try JSONEncoder().encode(credentials)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: credentialsAccount
        ]
        SecItemDelete(query as CFDictionary)
        var attributes = query
        attributes[kSecValueData as String] = data
        attributes[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(attributes as CFDictionary, nil)
        guard status == errSecSuccess else { throw APIError(message: "Unable to save the secure session.") }
    }

    static func read() throws -> SessionCredentials? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: credentialsAccount,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = item as? Data else {
            throw APIError(message: "Unable to read the secure session.")
        }
        return try JSONDecoder().decode(SessionCredentials.self, from: data)
    }

    static func clear() {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: credentialsAccount
        ]
        SecItemDelete(query as CFDictionary)
        clearActiveSMSSessionID()
        UserDefaults.standard.removeObject(forKey: baseURLKey)
    }

    // MARK: - Opaque SMS receiver session

    // Real card keys never live on the device. XIASS API stores them encrypted
    // server-side; the app keeps only this opaque session identifier so polling
    // can resume after it returns from the system browser.
    static func activeSMSSessionID() -> String? {
        do {
            guard let data = try readKeychainData(account: activeSMSSessionAccount),
                  let value = String(data: data, encoding: .utf8) else {
                return nil
            }
            let sessionID = value.trimmingCharacters(in: .whitespacesAndNewlines)
            return sessionID.isEmpty ? nil : sessionID
        } catch {
            return nil
        }
    }

    static func saveActiveSMSSessionID(_ sessionID: String) throws {
        let value = sessionID.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else {
            clearActiveSMSSessionID()
            return
        }
        try writeKeychainData(Data(value.utf8), account: activeSMSSessionAccount)
    }

    static func clearActiveSMSSessionID() {
        deleteKeychainData(account: activeSMSSessionAccount)
    }

    // Older App builds stored raw SMS card keys in Keychain. The server-backed
    // flow deliberately cannot migrate those values, so purge them on launch.
    static func purgeLegacySMSCardKeys() {
        for account in legacySMSCardKeyAccounts {
            deleteKeychainData(account: account)
        }
    }

    private static func readKeychainData(account: String) throws -> Data? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = item as? Data else {
            throw APIError(message: "Unable to access secure local storage.")
        }
        return data
    }

    private static func writeKeychainData(_ data: Data, account: String) throws {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account
        ]
        SecItemDelete(query as CFDictionary)
        var attributes = query
        attributes[kSecValueData as String] = data
        attributes[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(attributes as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw APIError(message: "Unable to save secure local storage.")
        }
    }

    private static func deleteKeychainData(account: String) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account
        ]
        SecItemDelete(query as CFDictionary)
    }
}
