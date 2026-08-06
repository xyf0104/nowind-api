import SwiftUI

enum AccountListFilter: String, CaseIterable, Identifiable {
    case all, normal, attention

    var id: String { rawValue }
    var title: String {
        switch self {
        case .all: return "全部"
        case .normal: return "正常"
        case .attention: return "需处理"
        }
    }
}

struct AccountsView: View {
    @EnvironmentObject private var session: AppSession
    @State private var accounts: [AdminAccount] = []
    @State private var searchText = ""
    @State private var platform = "all"
    @State private var filter: AccountListFilter
    @State private var isLoading = false
    @State private var showEditor = false
    @State private var error: ErrorMessage?

    init(initialFilter: AccountListFilter = .all) {
        _filter = State(initialValue: initialFilter)
    }

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                if isLoading && accounts.isEmpty {
                    ProgressView("正在读取账号…")
                        .frame(maxWidth: .infinity, minHeight: 180)
                }
                if !isLoading && accounts.isEmpty {
                    EmptyState(title: "暂无账号", systemImage: "person.crop.rectangle.stack", detail: "添加上游账号，或调整筛选条件。")
                }
                ForEach(accounts) { account in
                    NavigationLink { AccountDetailView(account: account) } label: {
                        GlassCard(tint: accountTint(account)) {
                            AccountRow(account: account)
                        }
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(16)
            .padding(.bottom, 72)
        }
        .navigationTitle("上游账号")
        .searchable(text: $searchText, prompt: "搜索账号名称或备注")
        .appScreenStyle()
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                Menu {
                    Picker("平台", selection: $platform) {
                        Text("全部平台").tag("all")
                        ForEach(PlatformOption.allCases) { option in
                            Text(option.title).tag(option.rawValue)
                        }
                    }
                    Picker("状态", selection: $filter) {
                        ForEach(AccountListFilter.allCases) { item in
                            Text(item.title).tag(item)
                        }
                    }
                } label: {
                    Image(systemName: "line.3.horizontal.decrease.circle")
                }
                .accessibilityLabel("平台筛选")
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button { showEditor = true } label: { Image(systemName: "plus") }
                    .accessibilityLabel("添加账号")
            }
        }
        .sheet(isPresented: $showEditor) {
            AccountEditorView(onSaved: { Task { await load() } })
        }
        .task(id: "\(searchText)|\(platform)|\(filter.rawValue)") {
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
            if platform != "all" { query.append(URLQueryItem(name: "platform", value: platform)) }
            if !searchText.trimmingCharacters(in: .whitespaces).isEmpty { query.append(URLQueryItem(name: "search", value: searchText)) }
            let page: Page<AdminAccount> = try await api.request(method: .get, path: "admin/accounts", query: query)
            accounts = page.items.filter { account in
                switch filter {
                case .all:
                    return true
                case .normal:
                    return account.status == "active" && account.schedulable != false && account.errorMessage?.isEmpty != false
                case .attention:
                    return account.status == "error" || account.errorMessage?.isEmpty == false || account.rateLimitedAt != nil || account.overloadUntil != nil || account.schedulable == false
                }
            }
        } catch {
            self.error = ErrorMessage(error, title: "无法读取账号")
        }
    }

    private func accountTint(_ account: AdminAccount) -> Color {
        if account.status == "error" || account.errorMessage?.isEmpty == false { return .red }
        if account.schedulable == false || account.rateLimitedAt != nil || account.overloadUntil != nil { return .orange }
        return account.platform == "anthropic" ? .orange : AppTheme.primary
    }
}

private struct AccountRow: View {
    let account: AdminAccount

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 8) {
                Text(account.name)
                    .font(.headline)
                    .lineLimit(1)
                Spacer(minLength: 8)
                StatusPill(text: account.healthLabel)
            }
            HStack(spacing: 8) {
                Text(account.platform.uppercased())
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.blue)
                Text(account.type)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                if let current = account.currentConcurrency, let limit = account.concurrency {
                    Text("\(current)/\(limit)")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Text("优先级 \(account.priority ?? 0)")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            if let error = account.errorMessage, !error.isEmpty {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 4)
    }
}

struct AccountDetailView: View {
    @EnvironmentObject private var session: AppSession
    @Environment(\.dismiss) private var dismiss
    @State private var current: AdminAccount
    @State private var isWorking = false
    @State private var message: ErrorMessage?
    @State private var showEditor = false
    @State private var showTest = false
    @State private var showDeleteConfirmation = false

    init(account: AdminAccount) {
        _current = State(initialValue: account)
    }

    var body: some View {
        List {
            Section("账号信息") {
                LabeledContent("平台", value: current.platform.uppercased())
                LabeledContent("凭证类型", value: current.type)
                LabeledContent("优先级", value: String(current.priority ?? 0))
                LabeledContent("并发", value: "\(current.currentConcurrency ?? 0) / \(current.concurrency ?? 0)")
                LabeledContent("费率倍率", value: DisplayFormat.decimal(current.rateMultiplier))
                LabeledContent("分组", value: current.groupIDs?.map(String.init).joined(separator: "、") ?? "未分组")
                if let notes = current.notes, !notes.isEmpty { LabeledContent("备注", value: notes) }
            }

            Section("调度") {
                Toggle("允许调度", isOn: Binding(
                    get: { current.schedulable ?? true },
                    set: { setSchedulable($0) }
                ))
                .disabled(isWorking)
                LabeledContent("当前状态") { StatusPill(text: current.healthLabel) }
                if let error = current.errorMessage, !error.isEmpty {
                    Text(error).foregroundStyle(.red)
                }
            }

            Section("常用操作") {
                Button { showTest = true } label: {
                    Label("测试账号", systemImage: "bolt.horizontal.circle")
                }
                Button { showEditor = true } label: {
                    Label("编辑账号与凭证", systemImage: "pencil")
                }
                Button { performAction("refresh", success: "已发起凭证刷新。") } label: {
                    Label("刷新凭证", systemImage: "arrow.triangle.2.circlepath")
                }
                Button { performAction("recover-state", success: "账号运行状态已恢复。") } label: {
                    Label("恢复运行状态", systemImage: "stethoscope")
                }
                if current.errorMessage?.isEmpty == false {
                    Button { performAction("clear-error", success: "账号错误状态已清除。") } label: {
                        Label("清除错误", systemImage: "xmark.circle")
                    }
                }
            }

            if let credentials = current.credentialsStatus, !credentials.isEmpty {
                Section("凭证状态") {
                    ForEach(credentials.keys.sorted(), id: \.self) { key in
                        LabeledContent(key, value: credentials[key] == true ? "已配置" : "缺失")
                    }
                }
            }

            Section {
                Button(role: .destructive) { showDeleteConfirmation = true } label: {
                    Label("删除账号", systemImage: "trash")
                }
            }
        }
        .navigationTitle(current.name)
        .navigationBarTitleDisplayMode(.inline)
        .appScreenStyle()
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                Button { showEditor = true } label: { Image(systemName: "pencil") }
                    .accessibilityLabel("编辑账号")
                Button { Task { await reload() } } label: { Image(systemName: "arrow.clockwise") }
                    .accessibilityLabel("刷新账号信息")
            }
        }
        .task { await reload() }
        .sheet(isPresented: $showEditor) {
            AccountEditorView(account: current, onSaved: { Task { await reload() } })
        }
        .sheet(isPresented: $showTest) { AccountTestSheet(account: current) }
        .confirmationDialog("确定删除此账号吗？", isPresented: $showDeleteConfirmation, titleVisibility: .visible) {
            Button("删除账号", role: .destructive) { deleteAccount() }
            Button("取消", role: .cancel) {}
        } message: {
            Text("删除后无法恢复，已绑定的分组关系也会移除。")
        }
        .requestError($message)
    }

    private func reload() async {
        guard let api = session.api else { return }
        do { current = try await api.request(method: .get, path: "admin/accounts/\(current.id)") }
        catch { message = ErrorMessage(error, title: "无法读取账号详情") }
    }

    private func setSchedulable(_ value: Bool) {
        Task {
            isWorking = true
            defer { isWorking = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                current = try await api.request(
                    method: .post,
                    path: "admin/accounts/\(current.id)/schedulable",
                    body: SchedulableRequest(schedulable: value)
                )
            } catch { message = ErrorMessage(error, title: "调度状态更新失败") }
        }
    }

    private func performAction(_ path: String, success: String) {
        Task {
            isWorking = true
            defer { isWorking = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let _: EmptyResponse = try await api.request(method: .post, path: "admin/accounts/\(current.id)/\(path)", body: EmptyResponse())
                message = ErrorMessage(APIError(message: success), title: "操作完成")
                await reload()
            } catch { message = ErrorMessage(error, title: "操作失败") }
        }
    }

    private func deleteAccount() {
        Task {
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let _: EmptyResponse = try await api.request(method: .delete, path: "admin/accounts/\(current.id)")
                dismiss()
            } catch { message = ErrorMessage(error, title: "删除账号失败") }
        }
    }
}
