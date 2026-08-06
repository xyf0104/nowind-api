import SwiftUI

struct DashboardView: View {
    @EnvironmentObject private var session: AppSession
    @State private var stats: DashboardStats?
    @State private var realtime: RealtimeMetrics?
    @State private var isLoading = true
    @State private var error: ErrorMessage?

    private let columns = [GridItem(.flexible(), spacing: 12), GridItem(.flexible(), spacing: 12)]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                header

                if isLoading && stats == nil {
                    ProgressView("正在读取运行数据…")
                        .frame(maxWidth: .infinity, minHeight: 240)
                } else {
                    sectionTitle("账号与用户")
                    LazyVGrid(columns: columns, spacing: 12) {
                        NavigationLink { AccountsView() } label: {
                            MetricTile(title: "上游账号", value: DisplayFormat.integer(stats?.totalAccounts), systemImage: "person.crop.rectangle.stack", tint: AppTheme.primary)
                        }
                        NavigationLink { AccountsView(initialFilter: .normal) } label: {
                            MetricTile(title: "正常账号", value: DisplayFormat.integer(stats?.normalAccounts), systemImage: "checkmark.seal", tint: .green)
                        }
                        NavigationLink { AccountsView(initialFilter: .attention) } label: {
                            MetricTile(title: "异常账号", value: DisplayFormat.integer(stats?.errorAccounts), systemImage: "exclamationmark.triangle", tint: .red)
                        }
                        NavigationLink { UsersView(initialStatus: "active") } label: {
                            MetricTile(title: "活跃用户", value: DisplayFormat.integer(stats?.activeUsers), systemImage: "person.2", tint: AppTheme.accent)
                        }
                    }
                    .buttonStyle(.plain)

                    sectionTitle("今日概况")
                    LazyVGrid(columns: columns, spacing: 12) {
                        NavigationLink { OperationsView() } label: {
                            MetricTile(title: "请求数", value: DisplayFormat.integer(stats?.todayRequests), systemImage: "arrow.left.arrow.right", tint: .cyan)
                        }
                        NavigationLink { OperationsView() } label: {
                            MetricTile(title: "Token", value: DisplayFormat.integer(stats?.todayTokens), systemImage: "text.word.spacing", tint: .indigo)
                        }
                        NavigationLink { PaymentOrdersView() } label: {
                            MetricTile(title: "实际成本", value: DisplayFormat.currency(stats?.todayActualCost), systemImage: "dollarsign.circle", tint: .orange)
                        }
                        NavigationLink { UsersView() } label: {
                            MetricTile(title: "新增用户", value: DisplayFormat.integer(stats?.todayNewUsers), systemImage: "person.badge.plus", tint: .mint)
                        }
                    }
                    .buttonStyle(.plain)

                    sectionTitle("实时状态")
                    LazyVGrid(columns: columns, spacing: 12) {
                        MetricTile(title: "处理中请求", value: DisplayFormat.integer(realtime?.activeRequests), systemImage: "bolt.horizontal", tint: AppTheme.primary)
                        MetricTile(title: "每分钟请求", value: DisplayFormat.decimal(realtime?.requestsPerMinute, digits: 1), systemImage: "gauge.with.dots.needle.33percent", tint: .teal)
                        MetricTile(title: "平均延迟", value: realtime?.averageResponseTime.map { "\(DisplayFormat.decimal($0, digits: 0)) ms" } ?? "--", systemImage: "timer", tint: .cyan)
                        MetricTile(title: "错误率", value: realtime?.errorRate.map { "\(DisplayFormat.decimal($0, digits: 2))%" } ?? "--", systemImage: "chart.line.downtrend.xyaxis", tint: .red)
                    }

                    if let stats, (stats.ratelimitAccounts ?? 0) > 0 || (stats.overloadAccounts ?? 0) > 0 {
                        NavigationLink { AccountsView(initialFilter: .attention) } label: {
                            GlassCard(tint: .orange) {
                                HStack(alignment: .top, spacing: 12) {
                                    GlassIcon(name: "exclamationmark.triangle.fill", tint: .orange)
                                    VStack(alignment: .leading, spacing: 5) {
                                        Text("调度需要关注").font(.headline)
                                        Text("\(DisplayFormat.integer(stats.ratelimitAccounts)) 个账号限流，\(DisplayFormat.integer(stats.overloadAccounts)) 个账号过载。")
                                            .font(.subheadline)
                                            .foregroundStyle(.secondary)
                                    }
                                    Spacer(minLength: 0)
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
            .padding(.bottom, 112)
        }
        .appScreenStyle()
        .navigationTitle("XIASS API")
        .navigationBarTitleDisplayMode(.large)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { Task { await load() } } label: { Image(systemName: "arrow.clockwise") }
                    .accessibilityLabel("刷新概览")
            }
        }
        .task { await load() }
        .refreshable { await load() }
        .requestError($error)
    }

    private var header: some View {
        GlassCard(tint: AppTheme.primary) {
            HStack(alignment: .center, spacing: 14) {
                GlassIcon(name: "shield.checkered", tint: AppTheme.primary)
                VStack(alignment: .leading, spacing: 4) {
                    Text(session.user?.email ?? "管理员")
                        .font(.headline)
                        .lineLimit(1)
                    Text(session.connectionAddress)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer(minLength: 8)
                VStack(alignment: .trailing, spacing: 4) {
                    Text("运行中")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.green)
                    Text("实时管理")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func sectionTitle(_ value: String) -> some View {
        Text(value)
            .font(.title3.weight(.bold))
            .padding(.horizontal, 4)
    }

    private func load() async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            async let statsRequest: DashboardStats = api.request(method: .get, path: "admin/dashboard/stats")
            async let realtimeRequest: RealtimeMetrics = api.request(method: .get, path: "admin/dashboard/realtime")
            stats = try await statsRequest
            realtime = try await realtimeRequest
        } catch {
            self.error = ErrorMessage(error)
        }
    }
}
