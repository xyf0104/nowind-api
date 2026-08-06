import SwiftUI
import AuthenticationServices

struct AccountCreationHubView: View {
    @Environment(\.dismiss) private var dismiss
    let onSaved: () -> Void

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(PlatformOption.allCases) { option in
                        NavigationLink {
                            OAuthAccountFlow(platform: option, onSaved: onSaved)
                        } label: {
                            HStack(spacing: 12) {
                                GlassIcon(name: PlatformStyle.icon(for: option.rawValue), tint: PlatformStyle.color(for: option.rawValue))
                                VStack(alignment: .leading, spacing: 2) {
                                    Text("添加 \(option.title) OAuth 账号")
                                    Text("生成授权链接，授权后自动导入凭证")
                                        .font(.caption).foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                } header: {
                    Text("OAuth 授权添加")
                } footer: {
                    Text("OAuth 授权在系统安全浏览器中完成，账号密钥不会长期保存在手机内。")
                }

                Section("手动凭证") {
                    NavigationLink {
                        AccountEditorView(onSaved: onSaved)
                    } label: {
                        Label("手动添加 API Key、Refresh Token 或 JSON 凭证", systemImage: "key.fill")
                    }
                }
            }
            .appScreenStyle()
            .navigationTitle("添加上游账号")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("关闭") { dismiss() } } }
        }
    }
}

private struct OAuthStartRequest: Encodable {
    let redirectURI: String?
    let oauthType: String?

    enum CodingKeys: String, CodingKey {
        case redirectURI = "redirect_uri"
        case oauthType = "oauth_type"
    }
}

private struct OAuthExchangeRequest: Encodable {
    let sessionID: String
    let code: String
    let state: String
    let redirectURI: String?
    let oauthType: String?

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case code, state
        case redirectURI = "redirect_uri"
        case oauthType = "oauth_type"
    }
}

struct OAuthAccountFlow: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss

    let platform: PlatformOption
    let onSaved: () -> Void

    @StateObject private var browser = OAuthBrowser()
    @State private var name = ""
    @State private var concurrency = 1
    @State private var priority = 0
    @State private var rateMultiplier = 1.0
    @State private var groups: [AdminGroup] = []
    @State private var selectedGroupIDs = Set<Int>()
    @State private var sessionID = ""
    @State private var state = ""
    @State private var authorizationCode = ""
    @State private var callbackText = ""
    @State private var isAuthorizing = false
    @State private var isSaving = false
    @State private var error: ErrorMessage?

    private var supportsCustomCallback: Bool { platform == .openai || platform == .grok }
    private var redirectURI: String? { supportsCustomCallback ? "xiassadmin://oauth" : nil }
    private var sortedGroups: [AdminGroup] { groups.filter { $0.platform == platform.rawValue }.sorted { ($0.rateMultiplier ?? 1) < ($1.rateMultiplier ?? 1) } }

    var body: some View {
        Form {
            Section("账号设置") {
                HStack(spacing: 10) {
                    GlassIcon(name: PlatformStyle.icon(for: platform.rawValue), tint: PlatformStyle.color(for: platform.rawValue))
                    VStack(alignment: .leading, spacing: 2) {
                        Text("\(platform.title) OAuth")
                        Text("授权完成后自动创建上游账号").font(.caption).foregroundStyle(.secondary)
                    }
                }
                TextField("账号名称（留空自动使用邮箱）", text: $name)
                IntegerInput(label: "并发数", value: $concurrency, range: 1...100, step: 1)
                IntegerInput(label: "优先级", value: $priority, range: 0...100, step: 1)
                DecimalInput(label: "费率倍率", value: $rateMultiplier, range: 0...100, step: 0.05, suffix: "x")
            }

            Section("绑定分组") {
                if sortedGroups.isEmpty {
                    Text("暂无 \(platform.title) 分组，授权后仍可在账号详情中绑定。")
                        .font(.footnote).foregroundStyle(.secondary)
                } else {
                    ForEach(sortedGroups) { group in
                        Toggle(isOn: Binding(get: { selectedGroupIDs.contains(group.id) }, set: { enabled in
                            if enabled { selectedGroupIDs.insert(group.id) } else { selectedGroupIDs.remove(group.id) }
                        })) {
                            HStack {
                                Text(group.name)
                                Spacer()
                                Text("\(DisplayFormat.decimal(group.rateMultiplier ?? 1))x")
                                    .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }

            Section("授权") {
                Button(isAuthorizing ? "正在打开授权页…" : "生成并打开授权链接") { beginAuthorization() }
                    .disabled(isAuthorizing || isSaving)
                if !sessionID.isEmpty {
                    Text("授权链接已生成。完成授权后会自动回到 App；如上游未回跳，可粘贴回调链接或授权码。")
                        .font(.footnote).foregroundStyle(.secondary)
                    TextField("粘贴回调链接或授权码", text: $callbackText, axis: .vertical)
                        .lineLimit(2...4)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    Button(isSaving ? "正在导入…" : "导入授权凭证") { importManualCallback() }
                        .disabled(isSaving || callbackText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
        .scrollDismissesKeyboard(.interactively)
        .appScreenStyle()
        .navigationTitle("添加 \(platform.title)")
        .navigationBarTitleDisplayMode(.inline)
        .task { await loadGroups() }
        .requestError($error)
    }

    private func loadGroups() async {
        guard let api = session.api else { return }
        do { groups = try await api.request(method: .get, path: "admin/groups/all", query: [URLQueryItem(name: "include_inactive", value: "true")]) }
        catch { self.error = ErrorMessage(error, title: "无法读取分组") }
    }

    private func beginAuthorization() {
        Task {
            isAuthorizing = true
            defer { isAuthorizing = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let request = OAuthStartRequest(redirectURI: redirectURI, oauthType: platform == .gemini ? "code_assist" : nil)
                let auth: OAuthAuthorization = try await api.request(method: .post, path: authorizationPath, body: request)
                sessionID = auth.sessionID
                state = auth.state ?? ""
                guard let url = URL(string: auth.authURL) else { throw APIError(message: "服务端返回的授权链接无效。") }
                feedback.notice("授权页已打开", detail: "请在系统安全浏览器完成 \(platform.title) 登录。")
                browser.open(url: url) { result in
                    switch result {
                    case .success(let callbackURL):
                        receive(callbackURL)
                    case .failure:
                        feedback.notice("等待授权结果", detail: "如页面未自动回到 App，请复制回调链接或授权码后在本页导入。")
                    }
                }
            } catch { self.error = ErrorMessage(error, title: "生成授权链接失败") }
        }
    }

    private func receive(_ url: URL) {
        let components = URLComponents(url: url, resolvingAgainstBaseURL: false)
        authorizationCode = components?.queryItems?.first(where: { $0.name == "code" })?.value ?? ""
        state = components?.queryItems?.first(where: { $0.name == "state" })?.value ?? state
        callbackText = url.absoluteString
        guard !authorizationCode.isEmpty else {
            feedback.notice("未识别授权码", detail: "请将授权页最后的回调链接粘贴到本页。")
            return
        }
        Task { await exchangeAndCreate() }
    }

    private func importManualCallback() {
        if let url = URL(string: callbackText), let components = URLComponents(url: url, resolvingAgainstBaseURL: false) {
            authorizationCode = components.queryItems?.first(where: { $0.name == "code" })?.value ?? callbackText
            state = components.queryItems?.first(where: { $0.name == "state" })?.value ?? state
        } else {
            authorizationCode = callbackText.trimmingCharacters(in: .whitespacesAndNewlines)
        }
        Task { await exchangeAndCreate() }
    }

    private func exchangeAndCreate() async {
        guard !sessionID.isEmpty, !authorizationCode.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            error = ErrorMessage(APIError(message: "请先生成授权链接并完成登录授权。"), title: "无法导入凭证")
            return
        }
        isSaving = true
        defer { isSaving = false }
        do {
            guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
            let body = OAuthExchangeRequest(sessionID: sessionID, code: authorizationCode.trimmingCharacters(in: .whitespacesAndNewlines), state: state, redirectURI: redirectURI, oauthType: platform == .gemini ? "code_assist" : nil)
            let token: OAuthTokenInfo = try await api.request(method: .post, path: exchangePath, body: body)
            guard !token.credentials.isEmpty else { throw APIError(message: "授权成功但没有返回可保存的凭证。") }
            let accountName = name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? (token.email ?? "\(platform.title) OAuth 账号") : name.trimmingCharacters(in: .whitespacesAndNewlines)
            let request = AccountCreateRequest(name: accountName, notes: "通过 XIASS 管理端 OAuth 导入", platform: platform.rawValue, type: "oauth", credentials: token.credentials, extra: token.name.map { ["name": .string($0)] }, concurrency: concurrency, priority: priority, rateMultiplier: rateMultiplier, loadFactor: nil, groupIDs: Array(selectedGroupIDs).sorted(), confirmMixedChannelRisk: true)
            let _: AdminAccount = try await api.request(method: .post, path: "admin/accounts", body: request)
            feedback.success("OAuth 账号已添加", detail: "\(accountName) 已完成授权并加入调度。")
            onSaved()
            dismiss()
        } catch { self.error = ErrorMessage(error, title: "OAuth 凭证导入失败") }
    }

    private var authorizationPath: String {
        switch platform {
        case .openai: return "admin/openai/generate-auth-url"
        case .anthropic: return "admin/accounts/generate-auth-url"
        case .gemini: return "admin/gemini/oauth/auth-url"
        case .antigravity: return "admin/antigravity/oauth/auth-url"
        case .grok: return "admin/grok/oauth/auth-url"
        }
    }

    private var exchangePath: String {
        switch platform {
        case .openai: return "admin/openai/exchange-code"
        case .anthropic: return "admin/accounts/exchange-code"
        case .gemini: return "admin/gemini/oauth/exchange-code"
        case .antigravity: return "admin/antigravity/oauth/exchange-code"
        case .grok: return "admin/grok/oauth/exchange-code"
        }
    }
}

@MainActor
private final class OAuthBrowser: NSObject, ObservableObject, ASWebAuthenticationPresentationContextProviding {
    private var authenticationSession: ASWebAuthenticationSession?

    func open(url: URL, completion: @escaping (Result<URL, Error>) -> Void) {
        let session = ASWebAuthenticationSession(url: url, callbackURLScheme: "xiassadmin") { callbackURL, error in
            if let callbackURL { completion(.success(callbackURL)) }
            else { completion(.failure(error ?? APIError(message: "授权窗口已关闭。"))) }
        }
        session.presentationContextProvider = self
        session.prefersEphemeralWebBrowserSession = false
        authenticationSession = session
        if !session.start() {
            completion(.failure(APIError(message: "无法启动系统授权窗口。")))
        }
    }

    func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        let windowScene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        return windowScene?.keyWindow ?? ASPresentationAnchor()
    }
}

struct ModelSelectionSheet: View {
    @Environment(\.dismiss) private var dismiss
    let models: [AccountTestModel]
    @Binding var selectedModel: String
    @State private var search = ""

    private var filteredModels: [AccountTestModel] {
        guard !search.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return models }
        return models.filter { $0.label.localizedCaseInsensitiveContains(search) || $0.id.localizedCaseInsensitiveContains(search) }
    }

    var body: some View {
        NavigationStack {
            List {
                Button { selectedModel = ""; dismiss() } label: {
                    HStack { Text("自动选择（推荐）"); Spacer(); if selectedModel.isEmpty { Image(systemName: "checkmark").foregroundStyle(AppTheme.primary) } }
                }
                ForEach(filteredModels) { item in
                    Button { selectedModel = item.id; dismiss() } label: {
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(item.displayName ?? item.id)
                                if item.displayName != nil && item.displayName != item.id { Text(item.id).font(.caption).foregroundStyle(.secondary) }
                            }
                            Spacer()
                            if selectedModel == item.id { Image(systemName: "checkmark").foregroundStyle(AppTheme.primary) }
                        }
                    }
                }
                if filteredModels.isEmpty { Text("没有匹配的模型").foregroundStyle(.secondary) }
            }
            .searchable(text: $search, prompt: "搜索模型")
            .appScreenStyle()
            .navigationTitle("选择测试模型")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } } }
        }
    }
}
