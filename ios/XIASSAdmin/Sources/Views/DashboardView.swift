import SwiftUI

struct DashboardView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter

    @State private var stats: DashboardStats?
    @State private var realtime: RealtimeMetrics?
    @State private var groupCount = 0
    @State private var recentCalls: [UsageLog] = []
    @State private var isLoading = true
    @State private var error: ErrorMessage?

    private let columns = [GridItem(.flexible(), spacing: 12), GridItem(.flexible(), spacing: 12)]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                serviceHeader

                if isLoading && stats == nil {
                    ProgressView("正在读取运行数据…")
                        .frame(maxWidth: .infinity, minHeight: 220)
                } else {
                    Text("高频管理")
                        .font(.title3.weight(.bold))
                        .padding(.horizontal, 4)

                    LazyVGrid(columns: columns, spacing: 12) {
                        NavigationLink { AccountsView() } label: {
                            MetricTile(title: "上游账号", value: DisplayFormat.integer(stats?.totalAccounts), systemImage: "person.crop.rectangle.stack", tint: AppTheme.primary)
                        }
                        NavigationLink { AccountsView(initialFilter: .normal) } label: {
                            MetricTile(title: "正常账号", value: DisplayFormat.integer(stats?.normalAccounts), systemImage: "checkmark.seal.fill", tint: .green)
                        }
                        NavigationLink { GroupsView() } label: {
                            MetricTile(title: "分组管理", value: DisplayFormat.integer(groupCount), systemImage: "square.stack.3d.up.fill", tint: .indigo)
                        }
                        NavigationLink { UsersView(initialStatus: "active") } label: {
                            MetricTile(title: "活跃用户", value: DisplayFormat.integer(stats?.activeUsers), systemImage: "person.2.fill", tint: AppTheme.accent)
                        }
                        NavigationLink { UsageLogsView() } label: {
                            MetricTile(title: "今日请求", value: DisplayFormat.integer(stats?.todayRequests), systemImage: "arrow.left.arrow.right.circle.fill", tint: .cyan)
                        }
                        NavigationLink { UsageLogsView() } label: {
                            MetricTile(title: "平均延迟", value: realtime?.averageResponseTime.map { "\(DisplayFormat.decimal($0, digits: 0)) ms" } ?? "--", systemImage: "timer", tint: .orange)
                        }
                        NavigationLink { PaymentOrdersView() } label: {
                            MetricTile(title: "充值记录", value: "查看", systemImage: "creditcard.and.123", tint: .mint)
                        }
                        RecentCallMetric(calls: recentCalls)
                        .gridCellColumns(2)
                    }
                    .buttonStyle(.plain)

                    if let stats, (stats.ratelimitAccounts ?? 0) > 0 || (stats.overloadAccounts ?? 0) > 0 || (stats.errorAccounts ?? 0) > 0 {
                        NavigationLink { AccountsView(initialFilter: .attention) } label: {
                            GlassCard(tint: .orange) {
                                HStack(alignment: .center, spacing: 12) {
                                    GlassIcon(name: "exclamationmark.triangle.fill", tint: .orange)
                                    VStack(alignment: .leading, spacing: 3) {
                                        Text("调度需要关注").font(.headline)
                                        Text("异常 \(stats.errorAccounts ?? 0) · 限流 \(stats.ratelimitAccounts ?? 0) · 过载 \(stats.overloadAccounts ?? 0)")
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                    }
                                    Spacer(minLength: 8)
                                    Image(systemName: "chevron.right").foregroundStyle(.tertiary)
                                }
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, 12)
            .padding(.bottom, 108)
        }
        .appScreenStyle()
        .navigationTitle("XIASS API")
        .navigationBarTitleDisplayMode(.large)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { Task { await load(notify: true) } } label: { Image(systemName: "arrow.clockwise") }
                    .accessibilityLabel("刷新概览")
            }
        }
        .task { await load() }
        .refreshable { await load(notify: true) }
        .requestError($error)
    }

    private var serviceHeader: some View {
        GlassCard(tint: AppTheme.primary) {
            HStack(alignment: .center, spacing: 12) {
                GlassIcon(name: "shield.checkered", tint: AppTheme.primary)
                VStack(alignment: .leading, spacing: 3) {
                    Text("管理控制台")
                        .font(.headline)
                    Text(session.connectionAddress)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer(minLength: 8)
                Label("已连接", systemImage: "checkmark.circle.fill")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.green)
            }
        }
    }

    private func load(notify: Bool = false) async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            async let statsRequest: DashboardStats = api.request(method: .get, path: "admin/dashboard/stats")
            async let realtimeRequest: RealtimeMetrics = api.request(method: .get, path: "admin/dashboard/realtime")
            async let groupRequest: Page<AdminGroup> = api.request(method: .get, path: "admin/groups", query: [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "1")])
            async let usageRequest: Page<UsageLog> = api.request(method: .get, path: "admin/usage", query: [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "5")])
            stats = try await statsRequest
            realtime = try await realtimeRequest
            groupCount = try await groupRequest.total
            recentCalls = try await usageRequest.items
            if notify { feedback.success("概览已刷新", detail: "已同步最新账号、用户、分组和调用状态。") }
        } catch {
            self.error = ErrorMessage(error, title: "无法读取概览")
        }
    }
}

private struct RecentCallMetric: View {
    let calls: [UsageLog]

    var body: some View {
        GlassCard(tint: .purple) {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 9) {
                    GlassIcon(name: "clock.arrow.circlepath", tint: .purple)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("最近调用")
                            .font(.headline)
                        Text(calls.isEmpty ? "暂无调用记录" : "最近 \(calls.count) 条调用")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Spacer(minLength: 8)
                }

                if calls.isEmpty {
                    NavigationLink { UsageLogsView() } label: {
                        HStack {
                            Text("查看调用记录")
                                .font(.subheadline.weight(.semibold))
                            Spacer()
                            Image(systemName: "chevron.right")
                                .font(.caption.weight(.bold))
                                .foregroundStyle(.tertiary)
                        }
                    }
                    .buttonStyle(.plain)
                } else {
                    VStack(spacing: 0) {
                        ForEach(calls.indices, id: \.self) { index in
                            NavigationLink { UsageLogDetailView(log: calls[index]) } label: {
                                RecentCallRow(call: calls[index])
                            }
                            .buttonStyle(.plain)

                            if index != calls.indices.last {
                                Divider()
                                    .padding(.leading, 50)
                            }
                        }
                    }

                    NavigationLink { UsageLogsView() } label: {
                        HStack(spacing: 6) {
                            Text("查看全部调用记录")
                                .font(.subheadline.weight(.semibold))
                            Spacer()
                            Image(systemName: "chevron.right")
                                .font(.caption.weight(.bold))
                        }
                        .foregroundStyle(AppTheme.primary)
                        .padding(.top, 6)
                    }
                    .buttonStyle(.plain)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

private struct RecentCallRow: View {
    let call: UsageLog

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            GlassIcon(name: "arrow.left.arrow.right.circle.fill", tint: statusTint)
                .scaleEffect(0.84)
                .frame(width: 38, height: 38)

            VStack(alignment: .leading, spacing: 5) {
                HStack(spacing: 8) {
                    Text(call.displayUser)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    Spacer(minLength: 6)
                    if let statusCode = call.statusCode {
                        Text(String(statusCode))
                            .font(.caption.monospacedDigit().weight(.bold))
                            .foregroundStyle(statusTint)
                    }
                }

                Text(modelTitle)
                    .font(.caption.weight(.semibold))
                    .lineLimit(1)

                HStack(spacing: 8) {
                    Text("推理 \(reasoningTitle(call.reasoningEffort))")
                    Text("\(call.displayGroup) -> \(call.displayAccount)")
                }
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)

                HStack(spacing: 10) {
                    Text("首字 \(call.firstTokenMS.map { "\($0) ms" } ?? "--")")
                    Text("总延迟 \(call.durationMS.map { "\($0) ms" } ?? "--")")
                    Spacer(minLength: 0)
                    Text(DisplayFormat.shortDate(call.createdAt))
                }
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.tertiary)
                .lineLimit(1)
            }

            Image(systemName: "chevron.right")
                .font(.caption.weight(.bold))
                .foregroundStyle(.tertiary)
                .padding(.top, 4)
        }
        .contentShape(Rectangle())
        .padding(.vertical, 10)
    }

    private var statusTint: Color {
        guard let statusCode = call.statusCode else { return .purple }
        return (200..<300).contains(statusCode) ? .green : .red
    }

    private var modelTitle: String {
        guard let upstream = call.upstreamModel, !upstream.isEmpty, upstream != call.model else {
            return call.model ?? "--"
        }
        return "\(call.model ?? "--") -> \(upstream)"
    }
}
