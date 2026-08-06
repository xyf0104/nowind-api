import SwiftUI

struct UserAPIKeysView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    let user: UserProfile
    @State private var keys: [APIKeyRecord]
    @State private var isLoading = false
    @State private var error: ErrorMessage?

    init(user: UserProfile, initialKeys: [APIKeyRecord] = []) {
        self.user = user
        _keys = State(initialValue: initialKeys)
    }

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                if isLoading && keys.isEmpty {
                    ProgressView("正在读取 API 密钥…").frame(maxWidth: .infinity, minHeight: 180)
                }
                if keys.isEmpty && !isLoading {
                    EmptyState(title: "暂无 API 密钥", systemImage: "key", detail: "该用户尚未创建 API 密钥。")
                }
                ForEach(keys) { key in
                    NavigationLink { APIKeyDetailView(key: key) } label: {
                        GlassCard(tint: key.status == "active" ? AppTheme.primary : .orange) {
                            HStack(alignment: .top, spacing: 12) {
                                GlassIcon(name: "key.fill", tint: key.status == "active" ? AppTheme.primary : .orange)
                                VStack(alignment: .leading, spacing: 5) {
                                    HStack {
                                        Text(key.name).font(.headline).lineLimit(1)
                                        Spacer(minLength: 8)
                                        StatusPill(text: key.status)
                                    }
                                    Text(key.maskedKey).font(.caption.monospaced()).foregroundStyle(.secondary)
                                    HStack(spacing: 10) {
                                        Text("分组 #\(key.groupID.map(String.init) ?? "未绑定")")
                                        Text("用量 \(DisplayFormat.decimal(key.quotaUsed)) / \(key.quota == 0 ? "不限" : DisplayFormat.decimal(key.quota))")
                                    }
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                }
                                Image(systemName: "chevron.right").font(.caption.weight(.bold)).foregroundStyle(.tertiary)
                            }
                        }
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(16)
            .padding(.bottom, 72)
        }
        .appScreenStyle()
        .navigationTitle("API 密钥")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { Task { await load(notify: true) } } label: { Image(systemName: "arrow.clockwise") }
            }
        }
        .task { await load() }
        .refreshable { await load(notify: true) }
        .requestError($error)
    }

    private func load(notify: Bool = false) async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            let page: Page<APIKeyRecord> = try await api.request(
                method: .get,
                path: "admin/users/\(user.id)/api-keys",
                query: [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "100")]
            )
            keys = page.items
            if notify { feedback.success("API 密钥已刷新", detail: "已同步 \(keys.count) 个 API 密钥。") }
        } catch {
            self.error = ErrorMessage(error, title: "无法读取 API 密钥")
        }
    }
}

struct APIKeyDetailView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss
    let key: APIKeyRecord
    @State private var groups: [AdminGroup] = []
    @State private var groupID: Int?
    @State private var isEditing = false
    @State private var isSaving = false
    @State private var error: ErrorMessage?

    init(key: APIKeyRecord) {
        self.key = key
        _groupID = State(initialValue: key.groupID)
    }

    var body: some View {
        List {
            Section("密钥信息") {
                LabeledContent("名称", value: key.name)
                LabeledContent("密钥", value: key.maskedKey)
                LabeledContent("状态") { StatusPill(text: key.status) }
                LabeledContent("当前并发", value: String(key.currentConcurrency ?? 0))
                LabeledContent("最近使用", value: DisplayFormat.shortDate(key.lastUsedAt))
            }
            Section("额度") {
                LabeledContent("额度上限", value: key.quota == 0 ? "不限制" : DisplayFormat.currency(key.quota))
                LabeledContent("已用额度", value: DisplayFormat.currency(key.quotaUsed))
            }
            Section("绑定分组") {
                if isEditing {
                    Picker("分组", selection: $groupID) {
                        Text("不绑定分组").tag(nil as Int?)
                        ForEach(groups) { group in
                            Text(group.name).tag(Optional(group.id))
                        }
                    }
                } else {
                    LabeledContent("分组", value: groupID.map { "#\($0)" } ?? "未绑定")
                }
            }
        }
        .appScreenStyle()
        .navigationTitle(key.name)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                if isEditing {
                    Button(isSaving ? "保存中…" : "保存") { save() }.disabled(isSaving)
                } else {
                    Button("编辑") { isEditing = true }
                }
            }
        }
        .task { await loadGroups() }
        .requestError($error)
    }

    private func loadGroups() async {
        guard let api = session.api else { return }
        do {
            groups = try await api.request(method: .get, path: "admin/groups/all", query: [URLQueryItem(name: "include_inactive", value: "true")])
        } catch { self.error = ErrorMessage(error, title: "无法读取分组") }
    }

    private func save() {
        Task {
            isSaving = true
            defer { isSaving = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let _: AdminAPIKeyUpdateResult = try await api.request(
                    method: .put,
                    path: "admin/api-keys/\(key.id)",
                    body: AdminAPIKeyUpdateRequest(groupID: groupID, resetRateLimitUsage: nil)
                )
                isEditing = false
                feedback.success("API 密钥已保存", detail: "绑定分组已同步到 XIASS API。")
            } catch { self.error = ErrorMessage(error, title: "API 密钥保存失败") }
        }
    }
}

struct BalanceHistoryView: View {
    @EnvironmentObject private var session: AppSession
    let user: UserProfile
    @State private var records: [BalanceHistoryItem]
    @State private var error: ErrorMessage?

    init(user: UserProfile, initialHistory: [BalanceHistoryItem] = []) {
        self.user = user
        _records = State(initialValue: initialHistory)
    }

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                if records.isEmpty {
                    EmptyState(title: "暂无余额记录", systemImage: "clock.arrow.circlepath", detail: "该用户暂无充值、余额或并发调整记录。")
                }
                ForEach(records) { item in
                    GlassCard(tint: item.value >= 0 ? .green : .orange) {
                        HStack(alignment: .top, spacing: 12) {
                            GlassIcon(name: item.value >= 0 ? "arrow.down.circle.fill" : "arrow.up.circle.fill", tint: item.value >= 0 ? .green : .orange)
                            VStack(alignment: .leading, spacing: 4) {
                                HStack {
                                    Text(balanceTypeTitle(item.type)).font(.headline)
                                    Spacer(minLength: 8)
                                    Text(item.value.formatted(.number.precision(.fractionLength(2))))
                                        .font(.headline.monospacedDigit())
                                        .foregroundStyle(item.value >= 0 ? .green : .orange)
                                }
                                if let notes = item.notes, !notes.isEmpty { Text(notes).font(.caption).foregroundStyle(.secondary) }
                                Text(DisplayFormat.shortDate(item.usedAt ?? item.createdAt)).font(.caption2).foregroundStyle(.tertiary)
                            }
                        }
                    }
                }
            }
            .padding(16)
            .padding(.bottom, 72)
        }
        .appScreenStyle()
        .navigationTitle("余额变动")
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .requestError($error)
    }

    private func load() async {
        guard records.isEmpty, let api = session.api else { return }
        do {
            let response: BalanceHistoryResponse = try await api.request(
                method: .get,
                path: "admin/users/\(user.id)/balance-history",
                query: [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "100")]
            )
            records = response.items
        } catch { self.error = ErrorMessage(error, title: "无法读取余额记录") }
    }
}

struct PaymentOrdersView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    let userID: Int?
    let title: String
    @State private var orders: [PaymentOrder] = []
    @State private var isLoading = false
    @State private var error: ErrorMessage?

    init(userID: Int? = nil, title: String = "充值记录") {
        self.userID = userID
        self.title = title
    }

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                if isLoading && orders.isEmpty { ProgressView("正在读取充值订单…").frame(maxWidth: .infinity, minHeight: 180) }
                if orders.isEmpty && !isLoading {
                    EmptyState(title: "暂无充值记录", systemImage: "creditcard", detail: "支付完成后的充值订单会显示在这里。")
                }
                ForEach(orders) { order in
                    NavigationLink { PaymentOrderDetailView(order: order) } label: {
                        PaymentOrderCard(order: order)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(16)
            .padding(.bottom, 72)
        }
        .appScreenStyle()
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { Task { await load(notify: true) } } label: { Image(systemName: "arrow.clockwise") }
            }
        }
        .task { await load() }
        .refreshable { await load(notify: true) }
        .requestError($error)
    }

    private func load(notify: Bool = false) async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            var query = [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "100")]
            if let userID { query.append(URLQueryItem(name: "user_id", value: String(userID))) }
            let page: Page<PaymentOrder> = try await api.request(method: .get, path: "admin/payment/orders", query: query)
            orders = page.items
            if notify { feedback.success("充值记录已刷新", detail: "已加载 \(orders.count) 条充值记录。") }
        } catch { self.error = ErrorMessage(error, title: "无法读取充值订单") }
    }
}

private struct PaymentOrderCard: View {
    let order: PaymentOrder

    var body: some View {
        GlassCard(tint: paymentStatusColor(order.status)) {
            HStack(alignment: .top, spacing: 12) {
                GlassIcon(name: "creditcard.fill", tint: paymentStatusColor(order.status))
                VStack(alignment: .leading, spacing: 5) {
                    HStack {
                        Text(order.userEmail ?? order.userName ?? "用户 #\(order.userID ?? 0)").font(.headline).lineLimit(1)
                        Spacer(minLength: 8)
                        StatusPill(text: order.status)
                    }
                    HStack(spacing: 8) {
                        Text(paymentTypeTitle(order.paymentType)).font(.caption.weight(.semibold))
                        Text(order.orderType == "subscription" ? "订阅" : "余额充值").font(.caption).foregroundStyle(.secondary)
                        Spacer()
                        Text(paymentAmount(order)).font(.subheadline.monospacedDigit().weight(.bold))
                    }
                    Text(DisplayFormat.shortDate(order.paidAt ?? order.createdAt)).font(.caption2).foregroundStyle(.tertiary)
                }
                Image(systemName: "chevron.right").font(.caption.weight(.bold)).foregroundStyle(.tertiary)
            }
        }
    }
}

private struct PaymentOrderDetailView: View {
    let order: PaymentOrder

    var body: some View {
        List {
            Section("订单信息") {
                LabeledContent("用户", value: order.userEmail ?? order.userName ?? "用户 #\(order.userID ?? 0)")
                LabeledContent("状态") { StatusPill(text: order.status) }
                LabeledContent("订单类型", value: order.orderType == "subscription" ? "订阅" : "余额充值")
                LabeledContent("支付方式", value: paymentTypeTitle(order.paymentType))
                LabeledContent("到账金额", value: paymentAmount(order))
                LabeledContent("订单号", value: order.outTradeNo ?? "--")
                if let paymentTradeNo = order.paymentTradeNo, !paymentTradeNo.isEmpty { LabeledContent("支付流水号", value: paymentTradeNo) }
            }
            Section("时间") {
                LabeledContent("创建时间", value: DisplayFormat.shortDate(order.createdAt))
                LabeledContent("支付时间", value: DisplayFormat.shortDate(order.paidAt))
                LabeledContent("完成时间", value: DisplayFormat.shortDate(order.completedAt))
            }
            if let reason = order.failedReason, !reason.isEmpty {
                Section("失败原因") { Text(reason).foregroundStyle(.red) }
            }
        }
        .appScreenStyle()
        .navigationTitle("充值订单")
        .navigationBarTitleDisplayMode(.inline)
    }
}

struct AdminAPIKeyUpdateRequest: Encodable {
    let groupID: Int?
    let resetRateLimitUsage: Bool?

    enum CodingKeys: String, CodingKey {
        case groupID = "group_id"
        case resetRateLimitUsage = "reset_rate_limit_usage"
    }
}

struct AdminAPIKeyUpdateResult: Decodable {
    let apiKey: APIKeyRecord

    enum CodingKeys: String, CodingKey { case apiKey = "api_key" }
}

private func paymentStatusColor(_ value: String) -> Color {
    switch value.lowercased() {
    case "paid", "completed": return .green
    case "failed", "cancelled", "expired": return .red
    default: return .orange
    }
}

private func paymentTypeTitle(_ value: String?) -> String {
    switch value?.lowercased() {
    case "alipay", "alipay_direct": return "支付宝"
    case "wxpay", "wxpay_direct": return "微信支付"
    case "stripe": return "Stripe"
    case "easypay": return "易支付"
    case "airwallex": return "Airwallex"
    default: return value?.isEmpty == false ? value! : "未知方式"
    }
}

private func paymentAmount(_ order: PaymentOrder) -> String {
    let amount = order.payAmount ?? order.amount
    let currency = (order.currency ?? "CNY").uppercased()
    return amount.formatted(.currency(code: currency).precision(.fractionLength(2)))
}

private func balanceTypeTitle(_ value: String) -> String {
    switch value {
    case "balance", "admin_balance": return "余额调整"
    case "affiliate_balance": return "邀请返利"
    case "concurrency", "admin_concurrency": return "并发调整"
    case "subscription": return "订阅变动"
    default: return value
    }
}
