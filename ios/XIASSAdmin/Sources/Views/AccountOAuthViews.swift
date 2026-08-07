import SwiftUI
import UIKit

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
    let proxyID: Int?
    let projectID: String?
    let tierID: String?

    enum CodingKeys: String, CodingKey {
        case redirectURI = "redirect_uri"
        case oauthType = "oauth_type"
        case proxyID = "proxy_id"
        case projectID = "project_id"
        case tierID = "tier_id"
    }
}

private struct OAuthExchangeRequest: Encodable {
    let sessionID: String
    let code: String
    let state: String
    let redirectURI: String?
    let oauthType: String?
    let proxyID: Int?
    let tierID: String?

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case code, state
        case redirectURI = "redirect_uri"
        case oauthType = "oauth_type"
        case proxyID = "proxy_id"
        case tierID = "tier_id"
    }
}

private struct ApplyOAuthCredentialsRequest: Encodable {
    let type: String
    let credentials: [String: JSONValue]
    let extra: [String: JSONValue]?
}

struct OAuthAccountFlow: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss

    let platform: PlatformOption
    let existingAccount: AdminAccount?
    let onSaved: () -> Void

    @State private var name = ""
    @State private var concurrency = 1
    @State private var priority = 0
    @State private var rateMultiplier = 1.0
    @State private var groups: [AdminGroup] = []
    @State private var selectedGroupIDs = Set<Int>()
    @State private var sessionID = ""
    @State private var state = ""
    @State private var authorizationURL: URL?
    @State private var authorizationCode = ""
    @State private var callbackText = ""
    @State private var isAuthorizing = false
    @State private var isOpeningBrowser = false
    @State private var isSaving = false
    @State private var error: ErrorMessage?

    init(platform: PlatformOption, existingAccount: AdminAccount? = nil, onSaved: @escaping () -> Void) {
        self.platform = platform
        self.existingAccount = existingAccount
        self.onSaved = onSaved
    }

    private var isReauthorization: Bool { existingAccount != nil }
    private var sortedGroups: [AdminGroup] { groups.filter { $0.platform == platform.rawValue }.sorted { ($0.rateMultiplier ?? 1) < ($1.rateMultiplier ?? 1) } }

    var body: some View {
        Form {
            if let existingAccount {
                Section {
                    HStack(spacing: 10) {
                        GlassIcon(name: PlatformStyle.icon(for: platform.rawValue), tint: PlatformStyle.color(for: platform.rawValue))
                        VStack(alignment: .leading, spacing: 2) {
                            Text(existingAccount.name)
                            Text("\(platform.title) OAuth · 账号 ID \(existingAccount.id)")
                                .font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    LabeledContent("当前优先级", value: String(existingAccount.priority ?? 0))
                    LabeledContent("绑定分组", value: existingAccount.groupIDs?.isEmpty == false ? "\(existingAccount.groupIDs?.count ?? 0) 个" : "未分组")
                } header: {
                    Text("重新授权账号")
                } footer: {
                    Text("重新授权只更新上游凭证并清除账号错误；名称、分组、并发、优先级、倍率和其他运行设置都会保留。")
                }
            } else {
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
            }

            Section("授权") {
                Button(isAuthorizing ? "正在生成授权链接…" : isReauthorization ? "生成重新授权链接" : "生成授权链接") { beginAuthorization() }
                    .disabled(isAuthorizing || isOpeningBrowser || isSaving)

                if let authorizationURL {
                    OAuthFlowStep(number: 1, title: "打开授权链接") {
                        Text(authorizationURL.absoluteString)
                            .font(.caption.monospaced())
                            .textSelection(.enabled)
                            .lineLimit(3...6)
                            .foregroundStyle(.secondary)

                        HStack(spacing: 12) {
                            Button {
                                UIPasteboard.general.string = authorizationURL.absoluteString
                                feedback.success("授权链接已复制", detail: "可粘贴到任意浏览器完成登录。")
                            } label: {
                                Label("复制链接", systemImage: "doc.on.doc")
                            }

                            Button(isOpeningBrowser ? "正在打开…" : "打开浏览器") {
                                openAuthorizationURL(authorizationURL)
                            }
                            .disabled(isOpeningBrowser || isSaving)
                        }
                    }

                    OAuthFlowStep(number: 2, title: "完成网页登录") {
                        Text("登录完成后复制浏览器最终地址中的回调链接或授权码。")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }

                    OAuthFlowStep(number: 3, title: "导入授权凭证") {
                        TextField("粘贴回调链接或授权码", text: $callbackText, axis: .vertical)
                            .lineLimit(2...4)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()

                        HStack(spacing: 12) {
                            Button {
                                guard let value = UIPasteboard.general.string?.trimmingCharacters(in: .whitespacesAndNewlines), !value.isEmpty else {
                                    feedback.notice("剪贴板没有可导入的内容", detail: "请先复制回调链接或授权码。")
                                    return
                                }
                                callbackText = value
                                importManualCallback()
                            } label: {
                                Label("从剪贴板导入", systemImage: "clipboard")
                            }
                            .disabled(isSaving)

                            Button(isSaving ? "正在导入…" : "完成授权") { importManualCallback() }
                                .disabled(isSaving || callbackText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        }
                    }

                    Button {
                        beginAuthorization()
                    } label: {
                        Label("重新生成链接", systemImage: "arrow.clockwise")
                    }
                    .disabled(isAuthorizing || isOpeningBrowser || isSaving)
                }
            }
        }
        .scrollDismissesKeyboard(.interactively)
        .appScreenStyle()
        .navigationTitle(isReauthorization ? "重新授权" : "添加 \(platform.title)")
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
                // Reuse the callback URI generated by XIASS API's web flow.
                // OpenAI's Codex OAuth client rejects an unregistered custom
                // scheme such as xiassadmin://oauth before the user can sign in.
                let request = OAuthStartRequest(
                    redirectURI: nil,
                    oauthType: oauthType,
                    proxyID: existingAccount?.proxyID,
                    projectID: geminiProjectID,
                    tierID: geminiTierID
                )
                let auth: OAuthAuthorization = try await api.request(method: .post, path: authorizationPath, body: request)
                sessionID = auth.sessionID
                state = auth.state ?? ""
                authorizationCode = ""
                callbackText = ""
                guard let url = URL(string: auth.authURL) else { throw APIError(message: "服务端返回的授权链接无效。") }
                authorizationURL = url
                feedback.notice(isReauthorization ? "重新授权链接已生成" : "授权链接已生成", detail: "复制链接或点击“打开浏览器”后完成 \(platform.title) 登录。")
            } catch { self.error = ErrorMessage(error, title: "生成授权链接失败") }
        }
    }

    private func openAuthorizationURL(_ url: URL) {
        isOpeningBrowser = true
        UIApplication.shared.open(url, options: [:]) { opened in
            DispatchQueue.main.async {
                isOpeningBrowser = false
                if opened {
                    feedback.notice("已在浏览器打开授权页", detail: "完成登录后回到此页，粘贴回调链接或授权码即可。")
                } else {
                    error = ErrorMessage(APIError(message: "无法打开系统浏览器。"), title: "打开授权页失败")
                }
            }
        }
    }

    private func receive(_ url: URL) {
        let components = URLComponents(url: url, resolvingAgainstBaseURL: false)
        if let providerError = components?.queryItems?.first(where: { $0.name == "error" })?.value {
            let description = components?.queryItems?.first(where: { $0.name == "error_description" })?.value
            error = ErrorMessage(APIError(message: description ?? providerError), title: "OAuth 授权未完成")
            return
        }
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
        let input = callbackText.trimmingCharacters(in: .whitespacesAndNewlines)
        if let url = URL(string: input), URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.isEmpty == false {
            receive(url)
            return
        }
        authorizationCode = input
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
            let body = OAuthExchangeRequest(
                sessionID: sessionID,
                code: authorizationCode.trimmingCharacters(in: .whitespacesAndNewlines),
                state: state,
                redirectURI: nil,
                oauthType: oauthType,
                proxyID: existingAccount?.proxyID,
                tierID: geminiTierID
            )
            let token: OAuthTokenInfo = try await api.request(method: .post, path: exchangePath, body: body)
            guard !token.credentials.isEmpty else { throw APIError(message: "授权成功但没有返回可保存的凭证。") }
            if let existingAccount {
                let request = ApplyOAuthCredentialsRequest(
                    type: existingAccount.type == "setup-token" ? "setup-token" : "oauth",
                    credentials: credentialsForPersistence(token),
                    extra: extraForPersistence(token)
                )
                let _: AdminAccount = try await api.request(method: .post, path: "admin/accounts/\(existingAccount.id)/apply-oauth-credentials", body: request)
                feedback.success("账号已重新授权", detail: "\(existingAccount.name) 的凭证已更新，原有账号设置保持不变。")
            } else {
                let accountName = name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? (token.email ?? "\(platform.title) OAuth 账号") : name.trimmingCharacters(in: .whitespacesAndNewlines)
                let request = AccountCreateRequest(name: accountName, notes: "通过 XIASS 管理端 OAuth 导入", platform: platform.rawValue, type: "oauth", credentials: credentialsForPersistence(token), extra: extraForPersistence(token), concurrency: concurrency, priority: priority, rateMultiplier: rateMultiplier, loadFactor: nil, groupIDs: Array(selectedGroupIDs).sorted(), confirmMixedChannelRisk: true)
                let _: AdminAccount = try await api.request(method: .post, path: "admin/accounts", body: request)
                feedback.success("OAuth 账号已添加", detail: "\(accountName) 已完成授权并加入调度。")
            }
            onSaved()
            dismiss()
        } catch { self.error = ErrorMessage(error, title: "OAuth 凭证导入失败") }
    }

    private var authorizationPath: String {
        switch platform {
        case .openai: return "admin/openai/generate-auth-url"
        case .anthropic: return existingAccount?.type == "setup-token" ? "admin/accounts/generate-setup-token-url" : "admin/accounts/generate-auth-url"
        case .gemini: return "admin/gemini/oauth/auth-url"
        case .antigravity: return "admin/antigravity/oauth/auth-url"
        case .grok: return "admin/grok/oauth/auth-url"
        }
    }

    private var exchangePath: String {
        switch platform {
        case .openai: return "admin/openai/exchange-code"
        case .anthropic: return existingAccount?.type == "setup-token" ? "admin/accounts/exchange-setup-token-code" : "admin/accounts/exchange-code"
        case .gemini: return "admin/gemini/oauth/exchange-code"
        case .antigravity: return "admin/antigravity/oauth/exchange-code"
        case .grok: return "admin/grok/oauth/exchange-code"
        }
    }

    private var oauthType: String? {
        guard platform == .gemini else { return nil }
        let configured = existingAccount?.credentials?["oauth_type"]?.stringValue
        return ["code_assist", "google_one", "ai_studio"].contains(configured ?? "") ? configured : "code_assist"
    }

    private var geminiProjectID: String? {
        guard oauthType == "code_assist" else { return nil }
        return existingAccount?.credentials?["project_id"]?.stringValue
    }

    private var geminiTierID: String? {
        existingAccount?.credentials?["tier_id"]?.stringValue
    }

    private func credentialsForPersistence(_ token: OAuthTokenInfo) -> [String: JSONValue] {
        let sensitiveKeys: Set<String> = [
            "access_token", "refresh_token", "id_token", "agent_private_key", "api_key",
            "session_key", "cookie", "aws_secret_access_key", "aws_session_token",
            "service_account_json", "service_account", "private_key"
        ]
        var credentials = (existingAccount?.credentials ?? [:]).filter { !sensitiveKeys.contains($0.key) }
        token.credentials.forEach { credentials[$0.key] = $0.value }
        if platform == .grok, credentials["base_url"] == nil {
            credentials["base_url"] = .string("https://cli-chat-proxy.grok.com/v1")
        }
        return credentials
    }

    private func extraForPersistence(_ token: OAuthTokenInfo) -> [String: JSONValue]? {
        var extra = token.extra ?? [:]
        if let name = token.name, !name.isEmpty { extra["name"] = .string(name) }
        if let email = token.email, !email.isEmpty { extra["email"] = .string(email) }
        return extra.isEmpty ? nil : extra
    }
}

private struct OAuthFlowStep<Content: View>: View {
    let number: Int
    let title: String
    @ViewBuilder let content: Content

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Text("\(number)")
                .font(.caption.bold())
                .foregroundStyle(.white)
                .frame(width: 24, height: 24)
                .background(AppTheme.primary, in: Circle())
            VStack(alignment: .leading, spacing: 10) {
                Text(title).font(.subheadline.weight(.semibold))
                content
            }
        }
        .padding(.vertical, 4)
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
