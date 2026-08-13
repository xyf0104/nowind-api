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
                if !keys.isEmpty { APIKeyTable(keys: keys) }
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

private struct APIKeyTable: View {
    let keys: [APIKeyRecord]

    var body: some View {
        AdminTableSurface(minWidth: 700) {
            AdminTableHeader {
                HStack(spacing: 12) {
                    AdminTableHeaderText(text: "名称 / 密钥", width: 190)
                    AdminTableHeaderText(text: "分组", width: 84)
                    AdminTableHeaderText(text: "状态", width: 72)
                    AdminTableHeaderText(text: "已用额度", width: 92, alignment: .trailing)
                    AdminTableHeaderText(text: "额度上限", width: 92, alignment: .trailing)
                    AdminTableHeaderText(text: "最近使用", width: 142)
                    Spacer(minLength: 0)
                }
            }
            ForEach(keys) { key in
                NavigationLink { APIKeyDetailView(key: key) } label: {
                    AdminTableRow {
                        HStack(spacing: 12) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(key.name).font(.subheadline.weight(.semibold)).lineLimit(1)
                                Text(key.maskedKey).font(.caption2.monospaced()).foregroundStyle(.secondary).lineLimit(1)
                            }
                            .frame(width: 190, alignment: .leading)
                            AdminTableText(text: key.groupID.map { "#\($0)" } ?? "未绑定", width: 84, color: .secondary)
                            StatusPill(text: key.status).frame(width: 72, alignment: .leading)
                            AdminTableText(text: DisplayFormat.currency(key.quotaUsed), width: 92, alignment: .trailing)
                            AdminTableText(text: key.quota == 0 ? "不限" : DisplayFormat.currency(key.quota), width: 92, alignment: .trailing)
                            AdminTableText(text: DisplayFormat.shortDate(key.lastUsedAt), width: 142, color: .secondary)
                            Image(systemName: "chevron.right").font(.caption.weight(.bold)).foregroundStyle(.tertiary)
                        }
                    }
                }
                .buttonStyle(.plain)
            }
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
                if !records.isEmpty { BalanceHistoryTable(records: records) }
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

private struct BalanceHistoryTable: View {
    let records: [BalanceHistoryItem]

    var body: some View {
        AdminTableSurface(minWidth: 690) {
            AdminTableHeader {
                HStack(spacing: 12) {
                    AdminTableHeaderText(text: "时间", width: 142)
                    AdminTableHeaderText(text: "类型", width: 104)
                    AdminTableHeaderText(text: "变动", width: 96, alignment: .trailing)
                    AdminTableHeaderText(text: "状态", width: 76)
                    AdminTableHeaderText(text: "备注", width: 218)
                    Spacer(minLength: 0)
                }
            }
            ForEach(records) { item in
                AdminTableRow {
                    HStack(spacing: 12) {
                        AdminTableText(text: DisplayFormat.shortDate(item.usedAt ?? item.createdAt), width: 142, color: .secondary)
                        AdminTableText(text: balanceTypeTitle(item.type), width: 104, weight: .semibold)
                        AdminTableText(
                            text: balanceHistoryValue(item),
                            width: 96,
                            alignment: .trailing,
                            color: item.value >= 0 ? .green : .orange,
                            weight: .semibold
                        )
                        StatusPill(text: item.status ?? "completed").frame(width: 76, alignment: .leading)
                        AdminTableText(text: item.notes ?? "--", width: 218, color: .secondary)
                        Spacer(minLength: 0)
                    }
                }
            }
        }
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
                if !orders.isEmpty { PaymentOrderTable(orders: orders) }
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

private struct PaymentOrderTable: View {
    let orders: [PaymentOrder]

    var body: some View {
        AdminTableSurface(minWidth: 844) {
            AdminTableHeader {
                HStack(spacing: 12) {
                    AdminTableHeaderText(text: "用户", width: 190)
                    AdminTableHeaderText(text: "时间", width: 140)
                    AdminTableHeaderText(text: "方式", width: 92)
                    AdminTableHeaderText(text: "类型", width: 86)
                    AdminTableHeaderText(text: "金额", width: 96, alignment: .trailing)
                    AdminTableHeaderText(text: "状态", width: 74)
                    AdminTableHeaderText(text: "订单号", width: 128)
                    Spacer(minLength: 0)
                }
            }
            ForEach(orders) { order in
                NavigationLink { PaymentOrderDetailView(order: order) } label: {
                    AdminTableRow {
                        HStack(spacing: 12) {
                            AdminTableText(text: order.userEmail ?? order.userName ?? "用户 #\(order.userID ?? 0)", width: 190, weight: .semibold)
                            AdminTableText(text: DisplayFormat.shortDate(order.paidAt ?? order.createdAt), width: 140, color: .secondary)
                            AdminTableText(text: paymentTypeTitle(order.paymentType), width: 92)
                            AdminTableText(text: order.orderType == "subscription" ? "订阅" : "余额充值", width: 86, color: .secondary)
                            AdminTableText(text: paymentAmount(order), width: 96, alignment: .trailing, weight: .semibold)
                            StatusPill(text: order.status).frame(width: 74, alignment: .leading)
                            AdminTableText(text: order.outTradeNo ?? "--", width: 128, color: .secondary)
                            Image(systemName: "chevron.right").font(.caption.weight(.bold)).foregroundStyle(.tertiary)
                        }
                    }
                }
                .buttonStyle(.plain)
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

private func balanceHistoryValue(_ item: BalanceHistoryItem) -> String {
    switch item.type {
    case "concurrency", "admin_concurrency":
        return item.value.formatted(.number.sign(strategy: .always()).precision(.fractionLength(0)))
    case "subscription":
        return item.value.formatted(.number.sign(strategy: .always()).precision(.fractionLength(0...2)))
    default:
        return DisplayFormat.currency(item.value)
    }
}
