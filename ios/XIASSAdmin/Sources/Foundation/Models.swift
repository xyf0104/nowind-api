import Foundation

enum JSONValue: Codable, Hashable, Sendable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case object([String: JSONValue])
    case array([JSONValue])
    case null

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([String: JSONValue].self) {
            self = .object(value)
        } else {
            self = .array(try container.decode([JSONValue].self))
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value): try container.encode(value)
        case .number(let value): try container.encode(value)
        case .bool(let value): try container.encode(value)
        case .object(let value): try container.encode(value)
        case .array(let value): try container.encode(value)
        case .null: try container.encodeNil()
        }
    }

    static func object(from json: String) throws -> [String: JSONValue] {
        let data = Data(json.utf8)
        let decoded = try JSONDecoder().decode(JSONValue.self, from: data)
        guard case .object(let values) = decoded else {
            throw APIError(message: "Credentials JSON must be an object.")
        }
        return values
    }

    var stringValue: String? {
        guard case .string(let value) = self else { return nil }
        return value
    }

    var doubleValue: Double? {
        guard case .number(let value) = self else { return nil }
        return value
    }

    var intValue: Int? {
        guard let doubleValue, doubleValue.rounded() == doubleValue else { return nil }
        return Int(doubleValue)
    }

    var boolValue: Bool? {
        guard case .bool(let value) = self else { return nil }
        return value
    }

    var objectValue: [String: JSONValue]? {
        guard case .object(let value) = self else { return nil }
        return value
    }
}

struct EmptyResponse: Codable {}

struct Page<Item: Decodable>: Decodable {
    let items: [Item]
    let total: Int
    let page: Int
    let pageSize: Int
    let pages: Int

    enum CodingKeys: String, CodingKey {
        case items, total, page, pages
        case pageSize = "page_size"
    }

    // Some admin endpoints intentionally omit pagination metadata when the
    // response is small. Keeping defaults here prevents a valid list from
    // making an entire native screen appear unavailable.
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        items = try container.decodeIfPresent([Item].self, forKey: .items) ?? []
        total = try container.decodeIfPresent(Int.self, forKey: .total) ?? items.count
        page = try container.decodeIfPresent(Int.self, forKey: .page) ?? 1
        pageSize = try container.decodeIfPresent(Int.self, forKey: .pageSize) ?? max(items.count, 1)
        pages = try container.decodeIfPresent(Int.self, forKey: .pages) ?? (items.isEmpty ? 0 : 1)
    }
}

struct UserProfile: Codable, Identifiable, Hashable {
    let id: Int
    let email: String
    let username: String?
    let role: String
    let balance: Double?
    let status: String?
    let concurrency: Int?
    let currentConcurrency: Int?
    let notes: String?
    let rpmLimit: Int?
    let allowedGroups: [Int]?
    let createdAt: String?
    let lastUsedAt: String?

    enum CodingKeys: String, CodingKey {
        case id, email, username, role, balance, status, concurrency, notes
        case currentConcurrency = "current_concurrency"
        case rpmLimit = "rpm_limit"
        case allowedGroups = "allowed_groups"
        case createdAt = "created_at"
        case lastUsedAt = "last_used_at"
    }
}

struct UserUsageSummary: Codable, Hashable {
    let totalRequests: Int?
    let totalCost: Double?
    let totalTokens: Int?

    enum CodingKeys: String, CodingKey {
        case totalRequests = "total_requests"
        case totalCost = "total_cost"
        case totalTokens = "total_tokens"
    }
}

struct APIKeyRecord: Codable, Identifiable, Hashable {
    let id: Int
    let name: String
    let key: String
    let groupID: Int?
    let status: String
    let quota: Double?
    let quotaUsed: Double?
    let currentConcurrency: Int?
    let lastUsedAt: String?
    let createdAt: String?

    enum CodingKeys: String, CodingKey {
        case id, name, key, status, quota
        case groupID = "group_id"
        case quotaUsed = "quota_used"
        case currentConcurrency = "current_concurrency"
        case lastUsedAt = "last_used_at"
        case createdAt = "created_at"
    }

    var maskedKey: String {
        guard key.count > 12 else { return key }
        return "\(key.prefix(7))…\(key.suffix(4))"
    }
}

struct BalanceHistoryItem: Codable, Identifiable, Hashable {
    let id: Int
    let code: String?
    let type: String
    let value: Double
    let status: String?
    let usedAt: String?
    let createdAt: String?
    let notes: String?

    enum CodingKeys: String, CodingKey {
        case id, code, type, value, status, notes
        case usedAt = "used_at"
        case createdAt = "created_at"
    }
}

struct BalanceHistoryResponse: Codable {
    let items: [BalanceHistoryItem]
    let total: Int?
    let totalRecharged: Double?

    enum CodingKeys: String, CodingKey {
        case items, total
        case totalRecharged = "total_recharged"
    }
}

struct PaymentOrder: Codable, Identifiable, Hashable {
    let id: Int
    let userID: Int?
    let userEmail: String?
    let userName: String?
    let amount: Double
    let payAmount: Double?
    let currency: String?
    let paymentType: String?
    let orderType: String?
    let status: String
    let outTradeNo: String?
    let paymentTradeNo: String?
    let paidAt: String?
    let completedAt: String?
    let createdAt: String?
    let failedReason: String?

    enum CodingKeys: String, CodingKey {
        case id, amount, currency, status
        case userID = "user_id"
        case userEmail = "user_email"
        case userName = "user_name"
        case payAmount = "pay_amount"
        case paymentType = "payment_type"
        case orderType = "order_type"
        case outTradeNo = "out_trade_no"
        case paymentTradeNo = "payment_trade_no"
        case paidAt = "paid_at"
        case completedAt = "completed_at"
        case createdAt = "created_at"
        case failedReason = "failed_reason"
    }
}

struct AuthPayload: Codable {
    let accessToken: String?
    let refreshToken: String?
    let expiresIn: Int?
    let tokenType: String?
    let user: UserProfile?
    let requires2FA: Bool?
    let tempToken: String?

    enum CodingKeys: String, CodingKey {
        case accessToken = "access_token"
        case refreshToken = "refresh_token"
        case expiresIn = "expires_in"
        case tokenType = "token_type"
        case user
        case requires2FA = "requires_2fa"
        case tempToken = "temp_token"
    }
}

struct SessionCredentials: Codable, Sendable {
    var accessToken: String
    var refreshToken: String?
    var expiresAt: Date?

    enum CodingKeys: String, CodingKey {
        case accessToken, refreshToken, expiresAt
    }
}

struct AdminAccount: Codable, Identifiable, Hashable {
    let id: Int
    let name: String
    let notes: String?
    let platform: String
    let type: String
    let proxyID: Int?
    let concurrency: Int?
    let currentConcurrency: Int?
    let priority: Int?
    let rateMultiplier: Double?
    let loadFactor: Int?
    let status: String
    let schedulable: Bool?
    let errorMessage: String?
    let lastUsedAt: String?
    let groupIDs: [Int]?
    let credentialsStatus: [String: Bool]?
    let credentials: [String: JSONValue]?
    let extra: [String: JSONValue]?
    let expiresAt: Int?
    let autoPauseOnExpired: Bool?
    let rateLimitedAt: String?
    let overloadUntil: String?
    let tempUnschedulableUntil: String?

    enum CodingKeys: String, CodingKey {
        case id, name, notes, platform, type, concurrency, priority, status, schedulable, credentials, extra
        case proxyID = "proxy_id"
        case rateMultiplier = "rate_multiplier"
        case loadFactor = "load_factor"
        case currentConcurrency = "current_concurrency"
        case errorMessage = "error_message"
        case lastUsedAt = "last_used_at"
        case groupIDs = "group_ids"
        case credentialsStatus = "credentials_status"
        case expiresAt = "expires_at"
        case autoPauseOnExpired = "auto_pause_on_expired"
        case rateLimitedAt = "rate_limited_at"
        case overloadUntil = "overload_until"
        case tempUnschedulableUntil = "temp_unschedulable_until"
    }

    var healthLabel: String {
        if status == "error" { return "Error" }
        if rateLimitedAt != nil { return "Rate limited" }
        if overloadUntil != nil { return "Overloaded" }
        if schedulable == false { return "Paused" }
        return status.capitalized
    }

    var displayEmail: String? {
        let keys = ["email", "email_address", "account_email", "user_email"]
        for values in [extra, credentials] {
            for key in keys {
                if let value = values?[key]?.stringValue?.trimmingCharacters(in: .whitespacesAndNewlines), !value.isEmpty {
                    return value
                }
            }
        }
        return nil
    }
}

struct AdminGroup: Decodable, Identifiable, Hashable {
    let id: Int
    let name: String
    let description: String?
    let platform: String
    let rateMultiplier: Double?
    let rpmLimit: Int?
    let isExclusive: Bool?
    let status: String
    let accountCount: Int?
    let activeAccountCount: Int?
    let rateLimitedAccountCount: Int?
    let sortOrder: Int?
    let subscriptionType: String?
    let costRatio: Double?
    let maxReasoningEffort: String?
    let rawPayload: [String: JSONValue]

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        rawPayload = try container.decode([String: JSONValue].self)
        id = rawPayload["id"]?.intValue ?? 0
        name = rawPayload["name"]?.stringValue ?? ""
        description = rawPayload["description"]?.stringValue
        platform = rawPayload["platform"]?.stringValue ?? ""
        rateMultiplier = rawPayload["rate_multiplier"]?.doubleValue
        rpmLimit = rawPayload["rpm_limit"]?.intValue
        isExclusive = rawPayload["is_exclusive"]?.boolValue
        status = rawPayload["status"]?.stringValue ?? "inactive"
        accountCount = rawPayload["account_count"]?.intValue
        activeAccountCount = rawPayload["active_account_count"]?.intValue
        rateLimitedAccountCount = rawPayload["rate_limited_account_count"]?.intValue
        sortOrder = rawPayload["sort_order"]?.intValue
        subscriptionType = rawPayload["subscription_type"]?.stringValue
        costRatio = rawPayload["cost_ratio"]?.doubleValue
        maxReasoningEffort = rawPayload["max_reasoning_effort"]?.stringValue
    }
}

struct DashboardStats: Codable {
    let totalUsers: Int?
    let todayNewUsers: Int?
    let activeUsers: Int?
    let totalAPIKeys: Int?
    let activeAPIKeys: Int?
    let totalAccounts: Int?
    let normalAccounts: Int?
    let errorAccounts: Int?
    let ratelimitAccounts: Int?
    let overloadAccounts: Int?
    let todayRequests: Int?
    let todayTokens: Int?
    let todayActualCost: Double?
    let averageDurationMS: Double?
    let rpm: Double?
    let tpm: Double?

    enum CodingKeys: String, CodingKey {
        case totalUsers = "total_users"
        case todayNewUsers = "today_new_users"
        case activeUsers = "active_users"
        case totalAPIKeys = "total_api_keys"
        case activeAPIKeys = "active_api_keys"
        case totalAccounts = "total_accounts"
        case normalAccounts = "normal_accounts"
        case errorAccounts = "error_accounts"
        case ratelimitAccounts = "ratelimit_accounts"
        case overloadAccounts = "overload_accounts"
        case todayRequests = "today_requests"
        case todayTokens = "today_tokens"
        case todayActualCost = "today_actual_cost"
        case averageDurationMS = "average_duration_ms"
        case rpm, tpm
    }
}

struct RealtimeMetrics: Codable {
    let activeRequests: Int?
    let requestsPerMinute: Double?
    let averageResponseTime: Double?
    let errorRate: Double?

    enum CodingKeys: String, CodingKey {
        case activeRequests = "active_requests"
        case requestsPerMinute = "requests_per_minute"
        case averageResponseTime = "average_response_time"
        case errorRate = "error_rate"
    }
}

struct UsageStats: Codable {
    let totalRequests: Int?
    let totalTokens: Int?
    let totalActualCost: Double?
    let averageDurationMS: Double?

    enum CodingKeys: String, CodingKey {
        case totalRequests = "total_requests"
        case totalTokens = "total_tokens"
        case totalActualCost = "total_actual_cost"
        case averageDurationMS = "average_duration_ms"
    }
}

struct UsageLog: Codable, Identifiable, Hashable {
    let id: Int?
    let requestID: String?
    let model: String?
    let upstreamModel: String?
    let userEmail: String?
    let groupName: String?
    let accountName: String?
    let totalTokens: Int?
    let actualCost: Double?
    let statusCode: Int?
    let createdAt: String?
    let reasoningEffort: String?
    let inboundEndpoint: String?
    let upstreamEndpoint: String?
    let durationMS: Int?
    let firstTokenMS: Int?
    let requestType: String?
    let stream: Bool?
    let serviceTier: String?
    let modelMappingChain: String?
    let accountRateMultiplier: Double?
    let user: UsageActor?
    let group: UsageGroupSummary?
    let account: UsageAccountSummary?

    enum CodingKeys: String, CodingKey {
        case id, model
        case requestID = "request_id"
        case upstreamModel = "upstream_model"
        case userEmail = "user_email"
        case groupName = "group_name"
        case accountName = "account_name"
        case totalTokens = "total_tokens"
        case actualCost = "actual_cost"
        case statusCode = "status_code"
        case createdAt = "created_at"
        case reasoningEffort = "reasoning_effort"
        case inboundEndpoint = "inbound_endpoint"
        case upstreamEndpoint = "upstream_endpoint"
        case durationMS = "duration_ms"
        case firstTokenMS = "first_token_ms"
        case requestType = "request_type"
        case stream
        case serviceTier = "service_tier"
        case modelMappingChain = "model_mapping_chain"
        case accountRateMultiplier = "account_rate_multiplier"
        case user, group, account
    }

    var stableID: String { "\(id ?? -1)-\(requestID ?? createdAt ?? model ?? "unknown")" }

    var displayUser: String {
        userEmail ?? user?.email ?? user?.username ?? "匿名用户"
    }

    var displayGroup: String {
        groupName ?? group?.name ?? "未分组"
    }

    var displayAccount: String {
        accountName ?? account?.name ?? "自动路由"
    }
}

struct UsageActor: Codable, Hashable {
    let id: Int?
    let email: String?
    let username: String?
}

struct UsageGroupSummary: Codable, Hashable {
    let id: Int?
    let name: String?
    let platform: String?
}

struct UsageAccountSummary: Codable, Hashable {
    let id: Int?
    let name: String?
}

struct AdminProxy: Codable, Identifiable, Hashable {
    let id: Int
    let name: String
    let protocolName: String?
    let host: String?
    let port: Int?
    let username: String?
    let status: String?
    let accountCount: Int?
    let latencyMS: Int?
    let latencyStatus: String?
    let latencyMessage: String?
    let country: String?
    let city: String?
    let qualityStatus: String?
    let qualityScore: Int?

    enum CodingKeys: String, CodingKey {
        case id, name, host, port, username, status, country, city
        case protocolName = "protocol"
        case accountCount = "account_count"
        case latencyMS = "latency_ms"
        case latencyStatus = "latency_status"
        case latencyMessage = "latency_message"
        case qualityStatus = "quality_status"
        case qualityScore = "quality_score"
    }
}

struct ProxyWriteRequest: Encodable {
    let name: String
    let protocolName: String
    let host: String
    let port: Int
    let username: String
    let password: String
    let status: String?

    enum CodingKeys: String, CodingKey {
        case name, host, port, username, password, status
        case protocolName = "protocol"
    }
}

struct OAuthAuthorization: Decodable {
    let authURL: String
    let sessionID: String

    enum CodingKeys: String, CodingKey {
        case authURL = "auth_url"
        case sessionID = "session_id"
    }

    var state: String? {
        URLComponents(string: authURL)?.queryItems?.first(where: { $0.name == "state" })?.value
    }
}

struct OAuthTokenInfo: Decodable {
    let rawPayload: [String: JSONValue]

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        rawPayload = try container.decode([String: JSONValue].self)
    }

    private func string(_ key: String) -> String? { rawPayload[key]?.stringValue }
    private func int(_ key: String) -> Int? { rawPayload[key]?.intValue }

    var accessToken: String? { string("access_token") }
    var refreshToken: String? { string("refresh_token") }
    var idToken: String? { string("id_token") }
    var email: String? { string("email") ?? string("email_address") }
    var name: String? { string("name") }
    var expiresAt: Int? { int("expires_at") }
    var clientID: String? { string("client_id") }

    var credentials: [String: JSONValue] {
        // Keep every provider-defined token field returned by the trusted
        // XIASS API exchange endpoint. This preserves provider-specific
        // fields such as project_id and organization_id without storing them
        // locally after account creation.
        rawPayload.filter { !["name", "extra", "message"].contains($0.key) }
    }

    var extra: [String: JSONValue]? { rawPayload["extra"]?.objectValue }
}

struct VersionInfo: Codable {
    let currentVersion: String
    let latestVersion: String
    let hasUpdate: Bool
    let buildType: String?
    let warning: String?

    enum CodingKeys: String, CodingKey {
        case currentVersion = "current_version"
        case latestVersion = "latest_version"
        case hasUpdate = "has_update"
        case buildType = "build_type"
        case warning
    }
}

struct UpdateResult: Codable {
    let message: String
    let needRestart: Bool?
    let updateInProgress: Bool?

    enum CodingKeys: String, CodingKey {
        case message
        case needRestart = "need_restart"
        case updateInProgress = "update_in_progress"
    }
}

struct ActionResult: Codable {
    let success: Bool?
    let message: String?
    let latencyMS: Int?

    enum CodingKeys: String, CodingKey {
        case success, message
        case latencyMS = "latency_ms"
    }
}

struct AccountPatch: Encodable {
    let status: String?
    let schedulable: Bool?
    let priority: Int?

    init(status: String? = nil, schedulable: Bool? = nil, priority: Int? = nil) {
        self.status = status
        self.schedulable = schedulable
        self.priority = priority
    }
}

struct SchedulableRequest: Encodable {
    let schedulable: Bool
}

struct AccountTestRequest: Encodable {
    let modelID: String
    let prompt: String
    let mode: String

    enum CodingKeys: String, CodingKey {
        case modelID = "model_id"
        case prompt, mode
    }
}

struct AccountTestModel: Codable, Identifiable, Hashable {
    let id: String
    let displayName: String?
    let type: String?

    enum CodingKeys: String, CodingKey {
        case id, type
        case displayName = "display_name"
    }

    var label: String {
        let title = displayName?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return title.isEmpty || title == id ? id : "\(title)（\(id)）"
    }
}

struct AccountTestEvent: Decodable, Identifiable, Hashable {
    let type: String
    let text: String?
    let model: String?
    let status: String?
    let code: String?
    let imageURL: String?
    let mimeType: String?
    let success: Bool?
    let error: String?

    enum CodingKeys: String, CodingKey {
        case type, text, model, status, code, success, error
        case imageURL = "image_url"
        case mimeType = "mime_type"
    }

    var id: String { "\(type)|\(text ?? imageURL ?? error ?? UUID().uuidString)" }
}

struct AccountTestResult {
    let events: [AccountTestEvent]

    var model: String? { events.first(where: { $0.type == "test_start" })?.model }
    var text: String { events.compactMap(\.text).joined() }
    var imageURLs: [String] { events.compactMap(\.imageURL) }
    var succeeded: Bool { events.contains { $0.type == "test_complete" && $0.success == true } }
}

struct AccountUpdateRequest: Encodable {
    let name: String?
    let notes: String?
    let type: String?
    let credentials: [String: JSONValue]?
    let extra: [String: JSONValue]?
    let concurrency: Int?
    let priority: Int?
    let rateMultiplier: Double?
    let loadFactor: Int?
    let status: String?
    let groupIDs: [Int]?
    let expiresAt: Int?
    let autoPauseOnExpired: Bool?
    let confirmMixedChannelRisk: Bool?

    enum CodingKeys: String, CodingKey {
        case name, notes, type, credentials, extra, concurrency, priority, status
        case rateMultiplier = "rate_multiplier"
        case loadFactor = "load_factor"
        case groupIDs = "group_ids"
        case expiresAt = "expires_at"
        case autoPauseOnExpired = "auto_pause_on_expired"
        case confirmMixedChannelRisk = "confirm_mixed_channel_risk"
    }
}

struct GroupUpdateRequest: Encodable {
    let name: String?
    let description: String?
    let platform: String?
    let rateMultiplier: Double?
    let rpmLimit: Int?
    let isExclusive: Bool?
    let status: String?
    let costRatio: Double?
    let maxReasoningEffort: String?

    enum CodingKeys: String, CodingKey {
        case name, description, platform, status
        case rateMultiplier = "rate_multiplier"
        case rpmLimit = "rpm_limit"
        case isExclusive = "is_exclusive"
        case costRatio = "cost_ratio"
        case maxReasoningEffort = "max_reasoning_effort"
    }
}

struct UserUpdateRequest: Encodable {
    let email: String?
    let password: String?
    let username: String?
    let notes: String?
    let role: String?
    let concurrency: Int?
    let rpmLimit: Int?
    let status: String?
    let allowedGroups: [Int]?

    enum CodingKeys: String, CodingKey {
        case email, password, username, notes, role, concurrency, status
        case rpmLimit = "rpm_limit"
        case allowedGroups = "allowed_groups"
    }
}

struct AccountCreateRequest: Encodable {
    let name: String
    let notes: String?
    let platform: String
    let type: String
    let credentials: [String: JSONValue]
    let extra: [String: JSONValue]?
    let concurrency: Int
    let priority: Int
    let rateMultiplier: Double?
    let loadFactor: Int?
    let groupIDs: [Int]
    let confirmMixedChannelRisk: Bool

    enum CodingKeys: String, CodingKey {
        case name, notes, platform, type, credentials, extra, concurrency, priority
        case rateMultiplier = "rate_multiplier"
        case loadFactor = "load_factor"
        case groupIDs = "group_ids"
        case confirmMixedChannelRisk = "confirm_mixed_channel_risk"
    }
}

struct GroupCreateRequest: Encodable {
    let name: String
    let description: String?
    let platform: String
    let rateMultiplier: Double
    let rpmLimit: Int
    let isExclusive: Bool

    enum CodingKeys: String, CodingKey {
        case name, description, platform
        case rateMultiplier = "rate_multiplier"
        case rpmLimit = "rpm_limit"
        case isExclusive = "is_exclusive"
    }
}

struct UserCreateRequest: Encodable {
    let email: String
    let password: String
    let username: String?
    let notes: String?
    let role: String?
    let balance: Double
    let concurrency: Int
    let rpmLimit: Int
    let allowedGroups: [Int]?

    enum CodingKeys: String, CodingKey {
        case email, password, username, notes, role, balance, concurrency
        case rpmLimit = "rpm_limit"
        case allowedGroups = "allowed_groups"
    }
}

struct BalanceChangeRequest: Encodable {
    let balance: Double
    let operation: String
    let notes: String
}
