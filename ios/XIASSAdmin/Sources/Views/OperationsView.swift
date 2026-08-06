import SwiftUI
import UIKit

private struct ConsoleDestination: Identifiable {
    let path: String
    let title: String
    var id: String { path }
}

struct OperationsView: View {
    @EnvironmentObject private var session: AppSession
    @State private var usageStats: UsageStats?
    @State private var usageLogs: [UsageLog] = []
    @State private var version: VersionInfo?
    @State private var isLoading = false
    @State private var isUpdating = false
    @State private var showUpdateConfirmation = false
    @State private var consoleDestination: ConsoleDestination?
    @State private var appRelease: GitHubRelease?
    @State private var isCheckingAppUpdate = false
    @State private var error: ErrorMessage?

    var body: some View {
        List {
            Section("完整网页后台") {
                Button { openConsole() } label: {
                    GlassActionRow("打开完整后台", detail: "完整网页模块，沉浸式全屏", icon: "rectangle.portrait.and.arrow.forward", tint: AppTheme.primary)
                }
                Button { openConsole(path: "/admin/channels/pricing", title: "渠道与模型价格") } label: {
                    GlassActionRow("渠道与模型价格", detail: "编辑模型、价格、路由和倍率", icon: "slider.horizontal.3", tint: .cyan)
                }
                Button { openConsole(path: "/admin/proxies", title: "代理与 FRP") } label: {
                    GlassActionRow("代理与 FRP", detail: "代理节点、连通性和运行状态", icon: "network", tint: .orange)
                }
                Button { openConsole(path: "/admin/audit-logs", title: "安全审计") } label: {
                    GlassActionRow("审计与系统设置", detail: "审计日志、风险控制、系统参数", icon: "checkmark.shield", tint: .mint)
                }
            }

            Section("资金与订单") {
                NavigationLink { PaymentOrdersView() } label: {
                    GlassActionRow("充值记录", detail: "用户、时间、金额和支付方式", icon: "creditcard.and.123", tint: .green)
                }
            }

            Section("今日用量") {
                LabeledContent("请求数", value: DisplayFormat.integer(usageStats?.totalRequests))
                LabeledContent("Token", value: DisplayFormat.integer(usageStats?.totalTokens))
                LabeledContent("实际成本", value: DisplayFormat.currency(usageStats?.totalActualCost))
                LabeledContent("平均延迟", value: usageStats?.averageDurationMS.map { "\(DisplayFormat.decimal($0, digits: 0)) ms" } ?? "--")
            }

            Section("最近调用") {
                if isLoading && usageLogs.isEmpty { ProgressView("正在读取用量…") }
                ForEach(usageLogs, id: \.stableID) { item in
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Text(item.model ?? "未知模型").font(.subheadline.weight(.medium))
                            Spacer()
                            Text(DisplayFormat.currency(item.actualCost)).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                        }
                        Text([item.userEmail, item.groupName, item.accountName].compactMap { $0 }.joined(separator: " · "))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                if !isLoading && usageLogs.isEmpty { Text("暂无最近用量记录").foregroundStyle(.secondary) }
            }

            Section("系统更新") {
                if let version {
                    LabeledContent("当前版本", value: version.currentVersion)
                    LabeledContent("最新版本", value: version.latestVersion)
                    if let warning = version.warning, !warning.isEmpty { Text(warning).font(.footnote).foregroundStyle(.orange) }
                    if version.hasUpdate {
                        Button(isUpdating ? "正在更新…" : "安装更新") { showUpdateConfirmation = true }
                            .disabled(isUpdating)
                    } else {
                        Label("已是最新版本", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                    }
                } else {
                    Button("检查更新") { Task { await loadVersion(force: true) } }
                }
                Button("刷新版本状态") { Task { await loadVersion(force: true) } }
                    .disabled(isUpdating)
            }

            Section("XIASS 管理端更新") {
                LabeledContent("当前版本", value: GitHubReleaseService.currentVersion)
                if let appRelease {
                    LabeledContent("GitHub 最新", value: appRelease.tagName)
                    if let otaURL = appRelease.otaInstallURL {
                        Button { UIApplication.shared.open(otaURL) } label: {
                            GlassActionRow("安装 GitHub 最新版本", detail: "通过已签名 OTA 安装包更新", icon: "arrow.down.app.fill", tint: .green)
                        }
                    } else if let installer = appRelease.installerAsset {
                        Link(destination: installer.browserDownloadURL) {
                            GlassActionRow("下载 GitHub 安装包", detail: "已签名 IPA，按设备分发方式安装", icon: "arrow.down.app.fill", tint: .green)
                        }
                    } else if let source = appRelease.sourceAsset {
                        Link(destination: source.browserDownloadURL) {
                            GlassActionRow("下载 Xcode 工程包", detail: "用本机 Team 签名后安装", icon: "hammer.fill", tint: .cyan)
                        }
                    } else {
                        Link(destination: appRelease.htmlURL) {
                            GlassActionRow("打开 GitHub 发布页", detail: "查看最新发布说明", icon: "safari", tint: .cyan)
                        }
                    }
                    Text(GitHubReleaseService.isNewer(appRelease.tagName) ? "检测到新版本，可直接获取 GitHub 安装包。" : "已是管理端最新版本。")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                } else {
                    Text("GitHub 尚未发布管理端安装包。发布名包含 XIASSAdmin 的 IPA、OTA manifest 或源码包后，这里会自动识别。")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                Button(isCheckingAppUpdate ? "正在检查 GitHub…" : "检测 GitHub 最新安装包") { checkAppUpdate() }
                    .disabled(isCheckingAppUpdate)
            }

            Section("会话") {
                Button(role: .destructive) { Task { await session.signOut() } } label: {
                    Label("退出登录", systemImage: "rectangle.portrait.and.arrow.right")
                }
            }
        }
        .navigationTitle("更多管理")
        .appScreenStyle()
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { Task { await load() } } label: { Image(systemName: "arrow.clockwise") }
                    .accessibilityLabel("刷新更多管理")
            }
        }
        .task { await load() }
        .refreshable { await load() }
        .alert("确定安装 XIASS API 更新吗？", isPresented: $showUpdateConfirmation) {
            Button("安装更新", role: .destructive) { performUpdate() }
            Button("取消", role: .cancel) {}
        } message: {
            Text("服务会短暂重启，进行中的请求可能需要自动重连。")
        }
        .fullScreenCover(item: $consoleDestination) { destination in
            if let url = session.adminWebURL(path: destination.path) {
                FullConsoleView(url: url, title: destination.title)
            }
        }
        .requestError($error)
    }

    private func load() async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            async let statsRequest: UsageStats = api.request(method: .get, path: "admin/usage/stats", query: [URLQueryItem(name: "period", value: "today")])
            async let logsRequest: Page<UsageLog> = api.request(method: .get, path: "admin/usage", query: [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "20")])
            usageStats = try await statsRequest
            usageLogs = try await logsRequest.items
            await loadVersion(force: false)
        } catch { self.error = ErrorMessage(error, title: "无法读取运行信息") }
    }

    private func loadVersion(force: Bool) async {
        guard let api = session.api else { return }
        do {
            let query = force ? [URLQueryItem(name: "force", value: "true")] : []
            version = try await api.request(method: .get, path: "admin/system/check-updates", query: query)
        } catch { self.error = ErrorMessage(error, title: "无法检查更新") }
    }

    private func performUpdate() {
        Task {
            isUpdating = true
            defer { isUpdating = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let result: UpdateResult = try await api.request(method: .post, path: "admin/system/update", body: EmptyResponse())
                error = ErrorMessage(APIError(message: result.message), title: "已开始更新")
                try? await Task.sleep(for: .seconds(6))
                await loadVersion(force: true)
            } catch { self.error = ErrorMessage(error, title: "更新失败") }
        }
    }

    private func openConsole(path: String = "/admin/dashboard", title: String = "XIASS 完整后台") {
        consoleDestination = ConsoleDestination(path: path, title: title)
    }

    private func checkAppUpdate() {
        Task {
            isCheckingAppUpdate = true
            defer { isCheckingAppUpdate = false }
            do {
                appRelease = try await GitHubReleaseService.latestIOSRelease()
            } catch {
                self.error = ErrorMessage(error, title: "无法检查 GitHub 更新")
            }
        }
    }
}
