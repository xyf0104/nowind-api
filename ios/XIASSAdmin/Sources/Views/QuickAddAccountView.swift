import SwiftUI
import UIKit
import AuthenticationServices

struct AccountEditorView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss

    let account: AdminAccount?
    let onSaved: () -> Void

    @State private var name: String
    @State private var notes: String
    @State private var platform: String
    @State private var type: String
    @State private var status: String
    @State private var credentialKey: String
    @State private var credentialValue = ""
    @State private var credentialJSON = "{\n  \n}"
    @State private var useCredentialJSON = false
    @State private var replaceCredentials = false
    @State private var extraJSON = "{\n  \n}"
    @State private var editExtra = false
    @State private var concurrency: Int
    @State private var priority: Int
    @State private var rateMultiplier: Double
    @State private var loadFactor: Int
    @State private var groups: [AdminGroup] = []
    @State private var selectedGroupIDs: Set<Int>
    @State private var isSaving = false
    @State private var error: ErrorMessage?

    init(account: AdminAccount? = nil, onSaved: @escaping () -> Void) {
        self.account = account
        self.onSaved = onSaved
        _name = State(initialValue: account?.name ?? "")
        _notes = State(initialValue: account?.notes ?? "")
        _platform = State(initialValue: account?.platform ?? "openai")
        _type = State(initialValue: account?.type ?? "oauth")
        _status = State(initialValue: account?.status ?? "active")
        _credentialKey = State(initialValue: Self.defaultCredentialKey(for: account?.type ?? "oauth"))
        _concurrency = State(initialValue: max(1, account?.concurrency ?? 1))
        _priority = State(initialValue: account?.priority ?? 0)
        _rateMultiplier = State(initialValue: account?.rateMultiplier ?? 1)
        _loadFactor = State(initialValue: account?.loadFactor ?? 0)
        _selectedGroupIDs = State(initialValue: Set(account?.groupIDs ?? []))
    }

    private var isEditing: Bool { account != nil }
    private var needsCredentialInput: Bool { !isEditing || replaceCredentials }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("账号名称", text: $name)
                    TextField("备注（可选）", text: $notes, axis: .vertical)
                        .lineLimit(2...4)
                    Picker("平台", selection: $platform) {
                        ForEach(PlatformOption.allCases) { option in
                            Text(option.title).tag(option.rawValue)
                        }
                    }
                    .disabled(isEditing)
                    Picker("凭证类型", selection: $type) {
                        Text("OAuth 登录凭证").tag("oauth")
                        Text("API Key").tag("apikey")
                        Text("Setup Token").tag("setup-token")
                        Text("上游中转").tag("upstream")
                        Text("Bedrock").tag("bedrock")
                        Text("服务账号").tag("service_account")
                    }
                } header: {
                    Text("基本信息")
                } footer: {
                    if isEditing { Text("平台由已有账号决定；如需切换平台，请新建账号后再迁移分组。") }
                }

                if isEditing {
                    Section("运行状态") {
                        Picker("账号状态", selection: $status) {
                            Text("启用").tag("active")
                            Text("停用").tag("inactive")
                            Text("错误状态").tag("error")
                        }
                    }
                }

                Section {
                    if isEditing {
                        Toggle("替换上游凭证", isOn: $replaceCredentials)
                    }
                    if needsCredentialInput {
                        Toggle("使用完整 JSON 凭证", isOn: $useCredentialJSON)
                        if useCredentialJSON {
                            JSONTextEditor(text: $credentialJSON, minHeight: 150)
                        } else {
                            TextField("凭证字段", text: $credentialKey)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                            SecureField("凭证内容", text: $credentialValue)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                        }
                    } else {
                        credentialStatus
                    }
                } header: {
                    Text("上游凭证")
                } footer: {
                    Text(needsCredentialInput ? "多字段凭证请使用 JSON，例如 access_token、refresh_token、base_url。保存后密钥不会保存在手机内。" : "为保护安全，已保存的密钥不会回显。需要变更时再开启替换。")
                }

                Section("调度与计费") {
                    IntegerInput(label: "并发数", value: $concurrency, range: 1...100, step: 1)
                    IntegerInput(label: "优先级", value: $priority, range: 0...100, step: 1)
                    DecimalInput(label: "费率倍率", value: $rateMultiplier, range: 0...100, step: 0.05, suffix: "x")
                    IntegerInput(label: "负载权重", value: $loadFactor, range: 0...10_000, step: 10, zeroLabel: "默认")
                }

                Section("绑定分组") {
                    if groups.isEmpty {
                        ProgressView("正在读取分组…")
                    } else {
                        ForEach(groups) { group in
                            Toggle(isOn: Binding(
                                get: { selectedGroupIDs.contains(group.id) },
                                set: { enabled in
                                    if enabled { selectedGroupIDs.insert(group.id) }
                                    else { selectedGroupIDs.remove(group.id) }
                                }
                            )) {
                                VStack(alignment: .leading, spacing: 3) {
                                    Text(group.name)
                                    HStack(spacing: 7) {
                                        PlatformBadge(platform: group.platform)
                                        Text("倍率 \(DisplayFormat.decimal(group.rateMultiplier ?? 1))")
                                            .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                                    }
                                }
                            }
                        }
                    }
                }

                Section {
                    Toggle("编辑扩展参数 JSON", isOn: $editExtra)
                    if editExtra {
                        JSONTextEditor(text: $extraJSON, minHeight: 150)
                    }
                } header: {
                    Text("高级参数")
                } footer: {
                    Text("与 XIASS API 高级参数对应。仅在明确需要时修改，避免覆盖运行时设置。")
                }
            }
            .scrollDismissesKeyboard(.interactively)
            .dismissKeyboardOnTap()
            .appScreenStyle()
            .navigationTitle(isEditing ? "编辑账号" : "添加账号")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isSaving ? "正在保存…" : "保存") { save() }
                        .disabled(isSaving || name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
            .task { await loadGroups() }
            .onChange(of: type) { value in
                credentialKey = Self.defaultCredentialKey(for: value)
            }
        }
        .requestError($error)
    }

    @ViewBuilder
    private var credentialStatus: some View {
        if let states = account?.credentialsStatus, !states.isEmpty {
            ForEach(states.keys.sorted(), id: \.self) { key in
                LabeledContent(key, value: states[key] == true ? "已配置" : "缺失")
            }
        } else {
            Text("此账号没有可显示的凭证状态。")
                .foregroundStyle(.secondary)
        }
    }

    private func loadGroups() async {
        guard let api = session.api else { return }
        do {
            groups = try await api.request(
                method: .get,
                path: "admin/groups/all",
                query: [URLQueryItem(name: "include_inactive", value: "true")]
            )
        } catch {
            self.error = ErrorMessage(error, title: "无法读取分组")
        }
    }

    private func credentials() throws -> [String: JSONValue]? {
        guard needsCredentialInput else { return nil }
        if useCredentialJSON {
            return try JSONValue.object(from: credentialJSON)
        }
        let key = credentialKey.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !key.isEmpty, !credentialValue.isEmpty else {
            throw APIError(message: "请填写凭证字段和凭证内容。")
        }
        return [key: .string(credentialValue)]
    }

    private func save() {
        Task {
            isSaving = true
            defer { isSaving = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let values = try credentials()
                let extra = try editExtra ? JSONValue.object(from: extraJSON) : nil
                if let account {
                    let request = AccountUpdateRequest(
                        name: name.trimmingCharacters(in: .whitespacesAndNewlines),
                        notes: notes.trimmingCharacters(in: .whitespacesAndNewlines),
                        type: type,
                        credentials: values,
                        extra: extra,
                        concurrency: concurrency,
                        priority: priority,
                        rateMultiplier: rateMultiplier,
                        loadFactor: loadFactor,
                        status: status,
                        groupIDs: Array(selectedGroupIDs).sorted(),
                        expiresAt: nil,
                        autoPauseOnExpired: nil,
                        confirmMixedChannelRisk: true
                    )
                    let _: AdminAccount = try await api.request(method: .put, path: "admin/accounts/\(account.id)", body: request)
                } else {
                    guard let values else { throw APIError(message: "请填写上游凭证。") }
                    let request = AccountCreateRequest(
                        name: name.trimmingCharacters(in: .whitespacesAndNewlines),
                        notes: notes.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : notes,
                        platform: platform,
                        type: type,
                        credentials: values,
                        extra: extra,
                        concurrency: concurrency,
                        priority: priority,
                        rateMultiplier: rateMultiplier,
                        loadFactor: loadFactor,
                        groupIDs: Array(selectedGroupIDs).sorted(),
                        confirmMixedChannelRisk: true
                    )
                    let _: AdminAccount = try await api.request(method: .post, path: "admin/accounts", body: request)
                }
                credentialValue = ""
                feedback.success(isEditing ? "账号已保存" : "账号已添加", detail: "凭证、调度和分组已同步到 XIASS API。")
                onSaved()
                dismiss()
            } catch {
                self.error = ErrorMessage(error, title: isEditing ? "账号保存失败" : "账号添加失败")
            }
        }
    }

    private static func defaultCredentialKey(for type: String) -> String {
        switch type {
        case "apikey", "upstream": return "api_key"
        case "setup-token": return "setup_token"
        case "service_account": return "service_account_json"
        default: return "refresh_token"
        }
    }
}

struct AccountTestSheet: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss
    let account: AdminAccount

    @State private var model = ""
    @State private var prompt = "hi"
    @State private var mode = "default"
    @State private var models: [AccountTestModel] = []
    @State private var isLoadingModels = false
    @State private var isTesting = false
    @State private var result: AccountTestResult?
    @State private var showModelPicker = false
    @State private var error: ErrorMessage?

    var body: some View {
        NavigationStack {
            List {
                Section("测试设置") {
                    Button { showModelPicker = true } label: {
                        HStack {
                            VStack(alignment: .leading, spacing: 3) {
                                Text("测试模型").foregroundStyle(.primary)
                                Text(selectedModelTitle).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                            }
                            Spacer()
                            Image(systemName: "chevron.up.chevron.down").foregroundStyle(AppTheme.primary)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    if isLoadingModels {
                        ProgressView("正在读取可用模型…")
                    } else if models.isEmpty {
                        Text("未读取到模型列表时将由服务器自动选择。")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                    TextField("测试提示词", text: $prompt, axis: .vertical)
                        .lineLimit(2...4)
                    Picker("测试模式", selection: $mode) {
                        Text("标准测试").tag("default")
                        Text("快速测试").tag("compact")
                    }
                }

                if isTesting {
                    Section { ProgressView("正在连接上游账号…") }
                }

                if let result {
                    Section("测试结果") {
                        LabeledContent("状态", value: result.succeeded ? "连接成功" : "已返回结果")
                        if let model = result.model, !model.isEmpty { LabeledContent("模型", value: model) }
                        if !result.text.isEmpty {
                            Text(result.text)
                                .textSelection(.enabled)
                                .font(.body)
                        }
                        ForEach(Array(result.imageURLs.enumerated()), id: \.offset) { _, value in
                            if let data = Self.imageData(from: value), let image = UIImage(data: data) {
                                Image(uiImage: image)
                                    .resizable()
                                    .scaledToFit()
                                    .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                            } else {
                                Text("测试已返回图片结果。")
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
            .navigationTitle("测试账号")
            .navigationBarTitleDisplayMode(.inline)
            .appScreenStyle()
            .scrollDismissesKeyboard(.interactively)
            .dismissKeyboardOnTap()
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("关闭") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isTesting ? "测试中…" : "开始测试") { test() }
                        .disabled(isTesting)
                }
            }
            .task { await loadModels() }
        }
        .requestError($error)
        .sheet(isPresented: $showModelPicker) {
            ModelSelectionSheet(models: models, selectedModel: $model)
        }
    }

    private var selectedModelTitle: String {
        guard !model.isEmpty else { return "自动选择（推荐）" }
        return models.first(where: { $0.id == model })?.label ?? model
    }

    private func test() {
        Task {
            isTesting = true
            result = nil
            defer { isTesting = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                result = try await api.testAccount(id: account.id, modelID: model, prompt: prompt, mode: mode)
                let testedModel = result?.model ?? (model.isEmpty ? "已由服务器自动选择模型" : model)
                feedback.success(result?.succeeded == true ? "账号连接成功" : "账号测试已完成", detail: testedModel)
            } catch {
                self.error = ErrorMessage(error, title: "账号测试失败")
            }
        }
    }

    private func loadModels() async {
        guard let api = session.api else { return }
        isLoadingModels = true
        defer { isLoadingModels = false }
        do {
            models = try await api.request(method: .get, path: "admin/accounts/\(account.id)/models")
        } catch {
            // A manual connection test must remain usable even if the upstream model list is unavailable.
            models = []
        }
    }

    private static func imageData(from value: String) -> Data? {
        guard let comma = value.firstIndex(of: ",") else { return nil }
        return Data(base64Encoded: String(value[value.index(after: comma)...]))
    }
}

enum PlatformOption: String, CaseIterable, Identifiable {
    case openai, anthropic, gemini, antigravity, grok
    var id: String { rawValue }
    var title: String {
        switch self {
        case .openai: return "OpenAI"
        case .anthropic: return "Anthropic"
        case .gemini: return "Gemini"
        case .antigravity: return "Antigravity"
        case .grok: return "Grok"
        }
    }
}

struct JSONTextEditor: View {
    @Binding var text: String
    let minHeight: CGFloat

    var body: some View {
        TextEditor(text: $text)
            .font(.system(.body, design: .monospaced))
            .frame(minHeight: minHeight)
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
    }
}
