import Foundation

@MainActor
final class AppSession: ObservableObject {
    enum Phase: Equatable {
        case restoring
        case signedOut
        case signedIn
    }

    @Published private(set) var phase: Phase = .restoring
    @Published private(set) var api: APIClient?
    @Published private(set) var user: UserProfile?
    @Published var pendingTwoFactorToken: String?
    @Published var connectionAddress = SecureStore.baseURL ?? "https://api.xiass.com"

    func restore() async {
        guard phase == .restoring else { return }
        guard let savedURL = SecureStore.baseURL,
              let baseURL = try? ConnectionURL.normalize(savedURL) else {
            phase = .signedOut
            return
        }
        let storedCredentials: SessionCredentials?
        do {
            storedCredentials = try SecureStore.read()
        } catch {
            phase = .signedOut
            return
        }
        guard let credentials = storedCredentials else {
            phase = .signedOut
            return
        }

        let restoredAPI = APIClient(baseURL: baseURL, credentials: credentials)
        do {
            let currentUser = try await restoredAPI.getCurrentUser()
            guard currentUser.role == "admin" else {
                await restoredAPI.logout()
                phase = .signedOut
                return
            }
            api = restoredAPI
            user = currentUser
            connectionAddress = savedURL
            phase = .signedIn
        } catch {
            await restoredAPI.logout()
            phase = .signedOut
        }
    }

    func signIn(address: String, email: String, password: String) async throws {
        let baseURL = try ConnectionURL.normalize(address)
        let nextAPI = APIClient(baseURL: baseURL)
        let response = try await nextAPI.login(email: email, password: password)

        if response.requires2FA == true, let token = response.tempToken {
            api = nextAPI
            pendingTwoFactorToken = token
            return
        }

        try await completeAuthentication(response, api: nextAPI, address: baseURL.absoluteString)
    }

    func completeTwoFactor(code: String) async throws {
        guard let api, let token = pendingTwoFactorToken else {
            throw APIError(message: "The two-factor challenge has expired. Please sign in again.")
        }
        let response = try await api.completeTwoFactor(tempToken: token, code: code)
        try await completeAuthentication(response, api: api, address: api.baseURL.absoluteString)
        pendingTwoFactorToken = nil
    }

    func cancelTwoFactor() {
        pendingTwoFactorToken = nil
        api = nil
    }

    func signOut() async {
        if let api { await api.logout() } else { SecureStore.clear() }
        api = nil
        user = nil
        pendingTwoFactorToken = nil
        phase = .signedOut
    }

    func adminWebURL(path: String = "/admin/dashboard") -> URL? {
        guard let base = try? ConnectionURL.normalize(connectionAddress) else { return nil }
        guard var components = URLComponents(url: base, resolvingAgainstBaseURL: false) else { return nil }

        // The API field accepts both the site origin and a full /api/v1 address.
        // The web console always lives at the site origin.
        components.path = ""
        components.query = nil
        components.fragment = nil
        guard let origin = components.url else { return nil }
        let normalizedPath = path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        return origin.appendingPathComponent(normalizedPath)
    }

    private func completeAuthentication(_ response: AuthPayload, api: APIClient, address: String) async throws {
        guard let accessToken = response.accessToken else {
            throw APIError(message: "XIASS API did not return an access token.")
        }
        try await api.establishSession(
            accessToken: accessToken,
            refreshToken: response.refreshToken,
            expiresIn: response.expiresIn
        )
        let currentUser = try await api.getCurrentUser()
        guard currentUser.role == "admin" else {
            await api.logout()
            throw APIError(message: "This account is not an XIASS API administrator.")
        }
        SecureStore.baseURL = address
        self.api = api
        self.user = currentUser
        self.connectionAddress = address
        self.phase = .signedIn
    }
}
