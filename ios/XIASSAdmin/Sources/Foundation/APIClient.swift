import Foundation

struct APIError: LocalizedError, Sendable {
    let message: String
    let statusCode: Int?

    init(message: String, statusCode: Int? = nil) {
        self.message = message
        self.statusCode = statusCode
    }

    var errorDescription: String? { message }
}

private struct APIEnvelope<Payload: Decodable>: Decodable {
    let code: Int
    let message: String?
    let data: Payload?
}

private struct RefreshRequest: Encodable {
    let refreshToken: String
    enum CodingKeys: String, CodingKey { case refreshToken = "refresh_token" }
}

enum HTTPMethod: String {
    case get = "GET"
    case post = "POST"
    case put = "PUT"
    case delete = "DELETE"
}

actor APIClient {
    let baseURL: URL
    private var credentials: SessionCredentials?
    private let decoder: JSONDecoder
    private let encoder: JSONEncoder

    init(baseURL: URL, credentials: SessionCredentials? = nil) {
        self.baseURL = baseURL
        self.credentials = credentials
        self.decoder = JSONDecoder()
        self.encoder = JSONEncoder()
    }

    func login(email: String, password: String) async throws -> AuthPayload {
        try await request(method: .post, path: "auth/login", body: ["email": email, "password": password], requiresAuth: false)
    }

    func completeTwoFactor(tempToken: String, code: String) async throws -> AuthPayload {
        try await request(
            method: .post,
            path: "auth/login/2fa",
            body: ["temp_token": tempToken, "totp_code": code],
            requiresAuth: false
        )
    }

    func establishSession(accessToken: String, refreshToken: String?, expiresIn: Int?) throws {
        let expiry = expiresIn.map { Date().addingTimeInterval(TimeInterval($0)) }
        let updated = SessionCredentials(accessToken: accessToken, refreshToken: refreshToken, expiresAt: expiry)
        credentials = updated
        try SecureStore.save(updated)
    }

    func getCurrentUser() async throws -> UserProfile {
        try await request(method: .get, path: "auth/me")
    }

    func logout() async {
        let refreshToken = credentials?.refreshToken
        if let refreshToken {
            let _: EmptyResponse? = try? await request(
                method: .post,
                path: "auth/logout",
                body: RefreshRequest(refreshToken: refreshToken)
            )
        }
        credentials = nil
        SecureStore.clear()
    }

    func testAccount(id: Int, modelID: String, prompt: String, mode: String) async throws -> AccountTestResult {
        let body = AccountTestRequest(modelID: modelID, prompt: prompt, mode: mode)
        var request = try makeRequest(method: .post, path: "admin/accounts/\(id)/test", query: [], body: body, requiresAuth: true)
        request.timeoutInterval = 180
        request.setValue("text/event-stream", forHTTPHeaderField: "Accept")

        let (data, response) = try await URLSession.shared.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw APIError(message: "XIASS API did not return an HTTP response.")
        }
        guard (200..<300).contains(httpResponse.statusCode) else {
            throw APIError(message: errorMessage(from: data) ?? "账号测试请求失败（HTTP \(httpResponse.statusCode)）。", statusCode: httpResponse.statusCode)
        }

        let lines = String(decoding: data, as: UTF8.self).split(whereSeparator: \.isNewline)
        var events: [AccountTestEvent] = []
        for line in lines {
            let value = line.trimmingCharacters(in: .whitespacesAndNewlines)
            guard value.hasPrefix("data:") else { continue }
            let payload = value.dropFirst(5).trimmingCharacters(in: .whitespaces)
            guard let event = try? decoder.decode(AccountTestEvent.self, from: Data(payload.utf8)) else { continue }
            events.append(event)
        }

        if let error = events.last(where: { $0.type == "error" })?.error, !error.isEmpty {
            throw APIError(message: error)
        }
        guard !events.isEmpty else {
            throw APIError(message: "测试服务没有返回结果。")
        }
        return AccountTestResult(events: events)
    }

    func request<Response: Decodable>(
        method: HTTPMethod,
        path: String,
        query: [URLQueryItem] = []
    ) async throws -> Response {
        try await perform(method: method, path: path, query: query, body: Optional<EmptyResponse>.none, requiresAuth: true, didRetryAfterRefresh: false)
    }

    func request<Response: Decodable, Body: Encodable>(
        method: HTTPMethod,
        path: String,
        query: [URLQueryItem] = [],
        body: Body,
        requiresAuth: Bool = true
    ) async throws -> Response {
        try await perform(method: method, path: path, query: query, body: body, requiresAuth: requiresAuth, didRetryAfterRefresh: false)
    }

    private func perform<Response: Decodable, Body: Encodable>(
        method: HTTPMethod,
        path: String,
        query: [URLQueryItem],
        body: Body?,
        requiresAuth: Bool,
        didRetryAfterRefresh: Bool
    ) async throws -> Response {
        var request = try makeRequest(method: method, path: path, query: query, body: body, requiresAuth: requiresAuth)
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw APIError(message: "XIASS API did not return an HTTP response.")
        }

        if httpResponse.statusCode == 401, requiresAuth, !didRetryAfterRefresh, credentials?.refreshToken != nil {
            try await refreshSession()
            request = try makeRequest(method: method, path: path, query: query, body: body, requiresAuth: true)
            let (retryData, retryResponse) = try await URLSession.shared.data(for: request)
            guard let retryHTTP = retryResponse as? HTTPURLResponse else {
                throw APIError(message: "XIASS API did not return an HTTP response.")
            }
            return try decode(retryData, response: retryHTTP)
        }

        return try decode(data, response: httpResponse)
    }

    private func refreshSession() async throws {
        guard let refreshToken = credentials?.refreshToken else {
            throw APIError(message: "Your administrator session has expired. Please sign in again.", statusCode: 401)
        }
        let response: AuthPayload = try await perform(
            method: .post,
            path: "auth/refresh",
            query: [],
            body: RefreshRequest(refreshToken: refreshToken),
            requiresAuth: false,
            didRetryAfterRefresh: true
        )
        guard let accessToken = response.accessToken else {
            throw APIError(message: "XIASS API did not return a refreshed session.", statusCode: 401)
        }
        try establishSession(
            accessToken: accessToken,
            refreshToken: response.refreshToken ?? refreshToken,
            expiresIn: response.expiresIn
        )
    }

    private func makeRequest<Body: Encodable>(
        method: HTTPMethod,
        path: String,
        query: [URLQueryItem],
        body: Body?,
        requiresAuth: Bool
    ) throws -> URLRequest {
        guard var components = URLComponents(url: apiURL(path: path), resolvingAgainstBaseURL: false) else {
            throw APIError(message: "Invalid XIASS API address.")
        }
        components.queryItems = query.isEmpty ? nil : query
        guard let url = components.url else { throw APIError(message: "Invalid XIASS API request URL.") }
        var request = URLRequest(url: url)
        request.httpMethod = method.rawValue
        request.timeoutInterval = method == .post && path.contains("system/update") ? 15 * 60 : 30
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.setValue("zh-CN", forHTTPHeaderField: "Accept-Language")
        request.setValue("1", forHTTPHeaderField: "X-Admin-UI")
        if requiresAuth, let token = credentials?.accessToken {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        if let body { request.httpBody = try encoder.encode(body) }
        return request
    }

    private func apiURL(path: String) -> URL {
        let root = baseURL.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        let prefix = root.hasSuffix("/api/v1") ? root : "\(root)/api/v1"
        return URL(string: "\(prefix)/\(path.trimmingCharacters(in: CharacterSet(charactersIn: "/")))")!
    }

    private func decode<Response: Decodable>(_ data: Data, response: HTTPURLResponse) throws -> Response {
        if !(200..<300).contains(response.statusCode) {
            throw APIError(message: errorMessage(from: data) ?? "XIASS API request failed (HTTP \(response.statusCode)).", statusCode: response.statusCode)
        }
        if Response.self == EmptyResponse.self, data.isEmpty {
            return EmptyResponse() as! Response
        }
        if let envelope = try? decoder.decode(APIEnvelope<Response>.self, from: data) {
            guard envelope.code == 0 else {
                throw APIError(message: envelope.message ?? "XIASS API rejected the request.", statusCode: response.statusCode)
            }
            guard let payload = envelope.data else {
                if Response.self == EmptyResponse.self { return EmptyResponse() as! Response }
                throw APIError(message: "XIASS API returned an empty response.")
            }
            return payload
        }
        return try decoder.decode(Response.self, from: data)
    }

    private func errorMessage(from data: Data) -> String? {
        guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
        return object["message"] as? String ?? object["detail"] as? String ?? object["error"] as? String
    }
}

enum ConnectionURL {
    static func normalize(_ text: String) throws -> URL {
        var trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.contains("://") { trimmed = "https://\(trimmed)" }
        trimmed = trimmed.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        guard let url = URL(string: trimmed), let scheme = url.scheme, ["https", "http"].contains(scheme), url.host != nil else {
            throw APIError(message: "Enter a valid XIASS API address, for example https://api.xiass.com")
        }
        return url
    }
}
