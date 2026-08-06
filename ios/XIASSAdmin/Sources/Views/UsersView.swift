import SwiftUI

struct UsersView: View {
    @EnvironmentObject private var session: AppSession
    @State private var users: [UserProfile] = []
    @State private var searchText = ""
    @State private var status: String
    @State private var isLoading = false
    @State private var showCreate = false
    @State private var error: ErrorMessage?

    init(initialStatus: String = "") {
        _status = State(initialValue: initialStatus)
    }

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                if isLoading && users.isEmpty {
                    ProgressView("正在读取用户…")
                        .frame(maxWidth: .infinity, minHeight: 180)
                }
                if !isLoading && users.isEmpty {
                    EmptyState(title: "暂无用户", systemImage: "person.2", detail: "创建用户后可在此设置余额、并发、RPM 和可用分组。")
                }
                ForEach(users) { user in
                    NavigationLink { UserDetailView(user: user) } label: {
                        GlassCard(tint: user.role == "admin" ? .indigo : AppTheme.accent) {
                            UserRow(user: user)
                        }
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(16)
            .padding(.bottom, 72)
        }
        .navigationTitle("用户管理")
        .searchable(text: $searchText, prompt: "搜索邮箱或用户名")
        .appScreenStyle()
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                Menu {
                    Picker("状态", selection: $status) {
                        Text("全部用户").tag("")
                        Text("正常").tag("active")
                        Text("停用").tag("disabled")
                    }
                } label: {
                    Image(systemName: "line.3.horizontal.decrease.circle")
                }
                .accessibilityLabel("用户状态筛选")
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button { showCreate = true } label: { Image(systemName: "person.badge.plus") }
                    .accessibilityLabel("创建用户")
            }
        }
        .sheet(isPresented: $showCreate) { UserEditorView(onSaved: { Task { await load() } }) }
        .task(id: "\(searchText)|\(status)") {
            try? await Task.sleep(for: .milliseconds(250))
            await load()
        }
        .refreshable { await load() }
        .requestError($error)
    }

    private func load() async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            var query = [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "100")]
            if !searchText.trimmingCharacters(in: .whitespaces).isEmpty { query.append(URLQueryItem(name: "search", value: searchText)) }
            if !status.isEmpty { query.append(URLQueryItem(name: "status", value: status)) }
            let page: Page<UserProfile> = try await api.request(method: .get, path: "admin/users", query: query)
            users = page.items
        } catch { self.error = ErrorMessage(error, title: "无法读取用户") }
    }
}

private struct UserRow: View {
    let user: UserProfile

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: user.role == "admin" ? "person.badge.shield.checkmark.fill" : "person.crop.circle.fill")
                .foregroundStyle(user.role == "admin" ? .indigo : .teal)
                .font(.title3)
            VStack(alignment: .leading, spacing: 4) {
                Text(user.email).font(.headline).lineLimit(1)
                Text(user.username?.isEmpty == false ? user.username! : "用户 #\(user.id)")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Spacer(minLength: 8)
            VStack(alignment: .trailing, spacing: 4) {
                Text(DisplayFormat.currency(user.balance)).font(.subheadline.monospacedDigit())
                StatusPill(text: user.status ?? "active")
            }
        }
        .padding(.vertical, 4)
    }
}

struct UserDetailView: View {
    @EnvironmentObject private var session: AppSession
    @Environment(\.dismiss) private var dismiss
    @State private var current: UserProfile
    @State private var showEditor = false
    @State private var showBalance = false
    @State private var showDeleteConfirmation = false
    @State private var apiKeys: [APIKeyRecord] = []
    @State private var usageSummary: UserUsageSummary?
    @State private var balanceHistory: BalanceHistoryResponse?
    @State private var isLoadingRelated = false
    @State private var error: ErrorMessage?

    init(user: UserProfile) {
        _current = State(initialValue: user)
    }

    var body: some View {
        List {
            Section("用户资料") {
                LabeledContent("邮箱", value: current.email)
                LabeledContent("用户名", value: current.username?.isEmpty == false ? current.username! : "未设置")
                LabeledContent("角色", value: current.role == "admin" ? "管理员" : "普通用户")
                LabeledContent("状态") { StatusPill(text: current.status ?? "active") }
                if let notes = current.notes, !notes.isEmpty { LabeledContent("备注", value: notes) }
            }

            Section("额度与限制") {
                LabeledContent("余额", value: DisplayFormat.currency(current.balance))
                LabeledContent("并发", value: "\(current.currentConcurrency ?? 0) / \(current.concurrency ?? 0)")
                LabeledContent("RPM 上限", value: (current.rpmLimit ?? 0) == 0 ? "不限制" : String(current.rpmLimit ?? 0))
                LabeledContent("可用分组", value: current.allowedGroups?.isEmpty == false ? current.allowedGroups!.map(String.init).joined(separator: "、") : "全部")
            }

            Section("用量与 API 密钥") {
                LabeledContent("累计请求", value: DisplayFormat.integer(usageSummary?.totalRequests))
                LabeledContent("累计 Token", value: DisplayFormat.integer(usageSummary?.totalTokens))
                LabeledContent("累计成本", value: DisplayFormat.currency(usageSummary?.totalCost))
                NavigationLink {
                    UserAPIKeysView(user: current, initialKeys: apiKeys)
                } label: {
                    LabeledContent("API 密钥", value: "\(apiKeys.count) 个")
                }
            }

            Section("余额与充值") {
                LabeledContent("累计充值", value: DisplayFormat.currency(balanceHistory?.totalRecharged))
                NavigationLink {
                    BalanceHistoryView(user: current, initialHistory: balanceHistory?.items ?? [])
                } label: {
                    LabeledContent("余额变动", value: "\(balanceHistory?.items.count ?? 0) 条")
                }
                NavigationLink {
                    PaymentOrdersView(userID: current.id, title: "\(current.email) 的充值")
                } label: {
                    Label("查看充值订单", systemImage: "creditcard.and.123")
                }
            }

            Section("操作") {
                Button { showEditor = true } label: { Label("编辑用户", systemImage: "pencil") }
                Button { showBalance = true } label: { Label("调整余额", systemImage: "creditcard") }
                Button((current.status ?? "active") == "active" ? "停用用户" : "启用用户") { toggleStatus() }
                Button(role: .destructive) { showDeleteConfirmation = true } label: { Label("删除用户", systemImage: "trash") }
            }
        }
        .navigationTitle(current.username?.isEmpty == false ? current.username! : current.email)
        .navigationBarTitleDisplayMode(.inline)
        .appScreenStyle()
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                Button { showEditor = true } label: { Image(systemName: "pencil") }
                    .accessibilityLabel("编辑用户")
                Button { Task { await reload() } } label: { Image(systemName: "arrow.clockwise") }
                    .accessibilityLabel("刷新用户信息")
            }
        }
        .task { await reload() }
        .sheet(isPresented: $showEditor) { UserEditorView(user: current, onSaved: { Task { await reload() } }) }
        .sheet(isPresented: $showBalance) { UserBalanceView(user: current, onSaved: { Task { await reload() } }) }
        .confirmationDialog("确定删除此用户吗？", isPresented: $showDeleteConfirmation, titleVisibility: .visible) {
            Button("删除用户", role: .destructive) { deleteUser() }
            Button("取消", role: .cancel) {}
        } message: {
            Text("删除后用户无法再登录，相关 API Key 也会受到影响。")
        }
        .requestError($error)
    }

    private func reload() async {
        guard let api = session.api else { return }
        do {
            current = try await api.request(method: .get, path: "admin/users/\(current.id)")
            await loadRelated()
        }
        catch { self.error = ErrorMessage(error, title: "无法读取用户详情") }
    }

    private func loadRelated() async {
        guard let api = session.api else { return }
        isLoadingRelated = true
        defer { isLoadingRelated = false }
        do {
            async let keysRequest: Page<APIKeyRecord> = api.request(
                method: .get,
                path: "admin/users/\(current.id)/api-keys",
                query: [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "100")]
            )
            async let usageRequest: UserUsageSummary = api.request(method: .get, path: "admin/users/\(current.id)/usage")
            async let balanceRequest: BalanceHistoryResponse = api.request(
                method: .get,
                path: "admin/users/\(current.id)/balance-history",
                query: [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "20")]
            )
            apiKeys = try await keysRequest.items
            usageSummary = try await usageRequest
            balanceHistory = try await balanceRequest
        } catch {
            // The profile remains useful even if an optional activity endpoint is disabled.
        }
    }

    private func toggleStatus() {
        Task {
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let body = UserUpdateRequest(email: nil, password: nil, username: nil, notes: nil, role: nil, concurrency: nil, rpmLimit: nil, status: (current.status ?? "active") == "active" ? "disabled" : "active", allowedGroups: nil)
                current = try await api.request(method: .put, path: "admin/users/\(current.id)", body: body)
            } catch { self.error = ErrorMessage(error, title: "用户状态更新失败") }
        }
    }

    private func deleteUser() {
        Task {
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let _: EmptyResponse = try await api.request(method: .delete, path: "admin/users/\(current.id)")
                dismiss()
            } catch { self.error = ErrorMessage(error, title: "删除用户失败") }
        }
    }
}

struct UserEditorView: View {
    @EnvironmentObject private var session: AppSession
    @Environment(\.dismiss) private var dismiss

    let user: UserProfile?
    let onSaved: () -> Void

    @State private var email: String
    @State private var password = ""
    @State private var username: String
    @State private var notes: String
    @State private var role: String
    @State private var status: String
    @State private var balance: Double
    @State private var concurrency: Int
    @State private var rpm: Int
    @State private var groups: [AdminGroup] = []
    @State private var selectedGroupIDs: Set<Int>
    @State private var isSaving = false
    @State private var error: ErrorMessage?

    init(user: UserProfile? = nil, onSaved: @escaping () -> Void) {
        self.user = user
        self.onSaved = onSaved
        _email = State(initialValue: user?.email ?? "")
        _username = State(initialValue: user?.username ?? "")
        _notes = State(initialValue: user?.notes ?? "")
        _role = State(initialValue: user?.role ?? "user")
        _status = State(initialValue: user?.status ?? "active")
        _balance = State(initialValue: user?.balance ?? 0)
        _concurrency = State(initialValue: max(1, user?.concurrency ?? 5))
        _rpm = State(initialValue: user?.rpmLimit ?? 0)
        _selectedGroupIDs = State(initialValue: Set(user?.allowedGroups ?? []))
    }

    private var isEditing: Bool { user != nil }

    var body: some View {
        NavigationStack {
            Form {
                Section("基本信息") {
                    TextField("邮箱", text: $email)
                        .textInputAutocapitalization(.never)
                        .keyboardType(.emailAddress)
                        .autocorrectionDisabled()
                    TextField("用户名（可选）", text: $username)
                    TextField("管理员备注（可选）", text: $notes, axis: .vertical)
                        .lineLimit(2...4)
                    SecureField(isEditing ? "新密码（留空不修改）" : "初始密码", text: $password)
                }

                Section("权限与状态") {
                    Picker("角色", selection: $role) {
                        Text("普通用户").tag("user")
                        Text("管理员").tag("admin")
                    }
                    if isEditing {
                        Picker("状态", selection: $status) {
                            Text("启用").tag("active")
                            Text("停用").tag("disabled")
                        }
                    }
                }

                Section("额度与限制") {
                    if !isEditing {
                        Stepper("初始余额：\(DisplayFormat.decimal(balance))", value: $balance, in: 0...100_000, step: 1)
                    }
                    Stepper("并发数：\(concurrency)", value: $concurrency, in: 1...100)
                    Stepper("RPM 上限：\(rpm == 0 ? "不限制" : String(rpm))", value: $rpm, in: 0...100_000, step: 10)
                }

                Section("允许使用的分组") {
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
                                Text(group.name)
                            }
                        }
                    }
                }
            }
            .scrollDismissesKeyboard(.interactively)
            .dismissKeyboardOnTap()
            .appScreenStyle()
            .navigationTitle(isEditing ? "编辑用户" : "创建用户")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isSaving ? "正在保存…" : "保存") { save() }
                        .disabled(isSaving || email.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || (!isEditing && password.count < 6))
                }
            }
            .task { await loadGroups() }
        }
        .requestError($error)
    }

    private func loadGroups() async {
        guard let api = session.api else { return }
        do { groups = try await api.request(method: .get, path: "admin/groups/all", query: [URLQueryItem(name: "include_inactive", value: "true")]) }
        catch { self.error = ErrorMessage(error, title: "无法读取分组") }
    }

    private func save() {
        Task {
            isSaving = true
            defer { isSaving = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let emailValue = email.trimmingCharacters(in: .whitespacesAndNewlines)
                if let user {
                    let request = UserUpdateRequest(
                        email: emailValue,
                        password: password.isEmpty ? nil : password,
                        username: username.trimmingCharacters(in: .whitespacesAndNewlines),
                        notes: notes.trimmingCharacters(in: .whitespacesAndNewlines),
                        role: role,
                        concurrency: concurrency,
                        rpmLimit: rpm,
                        status: status,
                        allowedGroups: Array(selectedGroupIDs).sorted()
                    )
                    let _: UserProfile = try await api.request(method: .put, path: "admin/users/\(user.id)", body: request)
                } else {
                    let request = UserCreateRequest(
                        email: emailValue,
                        password: password,
                        username: username.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : username,
                        notes: notes.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : notes,
                        role: role,
                        balance: balance,
                        concurrency: concurrency,
                        rpmLimit: rpm,
                        allowedGroups: Array(selectedGroupIDs).sorted()
                    )
                    let _: UserProfile = try await api.request(method: .post, path: "admin/users", body: request)
                }
                password = ""
                onSaved()
                dismiss()
            } catch { self.error = ErrorMessage(error, title: isEditing ? "用户保存失败" : "用户创建失败") }
        }
    }
}

struct UserBalanceView: View {
    @EnvironmentObject private var session: AppSession
    @Environment(\.dismiss) private var dismiss
    let user: UserProfile
    let onSaved: () -> Void
    @State private var amount = 0.0
    @State private var operation = "add"
    @State private var notes = ""
    @State private var isSaving = false
    @State private var error: ErrorMessage?

    var body: some View {
        NavigationStack {
            Form {
                Section(user.email) { LabeledContent("当前余额", value: DisplayFormat.currency(user.balance)) }
                Section("余额调整") {
                    Picker("操作", selection: $operation) {
                        Text("增加余额").tag("add")
                        Text("扣减余额").tag("subtract")
                        Text("设为固定余额").tag("set")
                    }
                    Stepper("金额：\(DisplayFormat.decimal(amount))", value: $amount, in: 0...100_000, step: 0.01)
                    TextField("操作备注", text: $notes, axis: .vertical)
                }
            }
            .navigationTitle("调整余额")
            .navigationBarTitleDisplayMode(.inline)
            .scrollDismissesKeyboard(.interactively)
            .dismissKeyboardOnTap()
            .appScreenStyle()
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isSaving ? "正在保存…" : "保存") { save() }
                        .disabled(isSaving || amount <= 0)
                }
            }
        }
        .requestError($error)
    }

    private func save() {
        Task {
            isSaving = true
            defer { isSaving = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let body = BalanceChangeRequest(balance: amount, operation: operation, notes: notes)
                let _: UserProfile = try await api.request(method: .post, path: "admin/users/\(user.id)/balance", body: body)
                onSaved()
                dismiss()
            } catch { self.error = ErrorMessage(error, title: "余额调整失败") }
        }
    }
}
