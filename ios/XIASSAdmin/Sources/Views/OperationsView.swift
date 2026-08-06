import SwiftUI

struct OperationsView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @AppStorage("xiass.appearance") private var appearanceRaw = AppAppearance.system.rawValue

    private var appearance: Binding<AppAppearance> {
        Binding(
            get: { AppAppearance(rawValue: appearanceRaw) ?? .system },
            set: { appearanceRaw = $0.rawValue }
        )
    }

    var body: some View {
        List {
            Section("管理账号") {
                LabeledContent("管理员", value: session.user?.email ?? "--")
                LabeledContent("服务地址", value: session.connectionAddress)
            }

            Section {
                Picker("主题", selection: appearance) {
                    ForEach(AppAppearance.allCases) { style in
                        Text(style.title).tag(style)
                    }
                }
            } header: {
                Text("外观")
            } footer: {
                Text("浅色和深色主题会同步调整模块、文字与输入框对比度。")
            }

            Section("原生管理模块") {
                NavigationLink { UsageLogsView() } label: {
                    GlassActionRow("最近调用", detail: "用户、模型、推理程度、链路与延迟", icon: "clock.arrow.circlepath", tint: .purple)
                }
                NavigationLink { PaymentOrdersView() } label: {
                    GlassActionRow("充值记录", detail: "用户、时间、金额和支付方式", icon: "creditcard.and.123", tint: .green)
                }
                NavigationLink { ProxyListView() } label: {
                    GlassActionRow("代理与 FRP", detail: "代理节点、连通性、账号绑定和编辑", icon: "network", tint: .orange)
                }
                NavigationLink { ModelPricingLookupView() } label: {
                    GlassActionRow("模型价格", detail: "查询模型默认价格与计费字段", icon: "tag.fill", tint: .cyan)
                }
                NavigationLink { SystemSettingsView() } label: {
                    GlassActionRow("系统配置", detail: "原生编辑 XIASS API 系统设置 JSON", icon: "slider.horizontal.3", tint: .indigo)
                }
            }

            Section("更新") {
                NavigationLink { UpdateCenterView() } label: {
                    GlassActionRow("更新中心", detail: "XIASS API 服务端与 GitHub 管理端安装包", icon: "arrow.triangle.2.circlepath.circle.fill", tint: AppTheme.primary)
                }
            }

            Section("会话") {
                Button(role: .destructive) {
                    Task {
                        await session.signOut()
                        feedback.success("已退出登录", detail: "本机管理会话已安全清除。")
                    }
                } label: {
                    Label("退出登录", systemImage: "rectangle.portrait.and.arrow.right")
                }
            }
        }
        .navigationTitle("设置")
        .appScreenStyle()
    }
}

struct UpdateCenterView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter

    @State private var version: VersionInfo?
    @State private var appRelease: GitHubRelease?
    @State private var isChecking = false
    @State private var isUpdating = false
    @State private var showServerConfirmation = false
    @State private var error: ErrorMessage?

    var body: some View {
        List {
            Section("XIASS API 服务端") {
                LabeledContent("当前版本", value: version?.currentVersion ?? "正在读取")
                LabeledContent("最新版本", value: version?.latestVersion ?? "正在读取")
                if let warning = version?.warning, !warning.isEmpty { Text(warning).font(.footnote).foregroundStyle(.orange) }
                if version?.hasUpdate == true {
                    Button(isUpdating ? "正在安装更新…" : "安装服务端更新") { showServerConfirmation = true }
                        .disabled(isUpdating)
                } else if version != nil {
                    Label("服务端已是最新版本", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                }
            }

            Section("XIASS 管理端") {
                LabeledContent("当前版本", value: GitHubReleaseService.currentVersion)
                if let release = appRelease {
                    LabeledContent("GitHub 最新", value: release.tagName)
                    if GitHubReleaseService.isNewer(release.tagName) {
                        if let otaURL = release.otaInstallURL {
                            Button { UIApplication.shared.open(otaURL) } label: { Label("安装 GitHub 最新版本", systemImage: "arrow.down.app.fill") }
                        } else {
                            Link("打开 GitHub 发布页", destination: release.htmlURL)
                        }
                    } else {
                        Label("管理端已是最新版本", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                    }
                } else {
                    Text("尚未读取到管理端发布包。")
                        .font(.footnote).foregroundStyle(.secondary)
                }
            }

            Section {
                Button(isChecking ? "正在检查全部更新…" : "检查全部更新") { Task { await checkUpdates(notify: true) } }
                    .disabled(isChecking || isUpdating)
            }
        }
        .appScreenStyle()
        .navigationTitle("更新中心")
        .navigationBarTitleDisplayMode(.inline)
        .task { await checkUpdates() }
        .refreshable { await checkUpdates(notify: true) }
        .alert("安装 XIASS API 服务端更新？", isPresented: $showServerConfirmation) {
            Button("安装更新", role: .destructive) { performServerUpdate() }
            Button("取消", role: .cancel) {}
        } message: { Text("服务会短暂重启，进行中的请求可能需要自动重连。") }
        .requestError($error)
    }

    private func checkUpdates(notify: Bool = false) async {
        guard let api = session.api else { return }
        isChecking = true
        defer { isChecking = false }
        do {
            async let server: VersionInfo = api.request(method: .get, path: "admin/system/check-updates", query: [URLQueryItem(name: "force", value: "true")])
            async let app = GitHubReleaseService.latestIOSRelease()
            version = try await server
            appRelease = try await app
            if notify {
                let serverUpdate = version?.hasUpdate == true
                let appUpdate = appRelease.map { GitHubReleaseService.isNewer($0.tagName) } ?? false
                if serverUpdate || appUpdate {
                    feedback.notice("发现可用更新", detail: "\(serverUpdate ? "服务端" : "")\(serverUpdate && appUpdate ? " 和 " : "")\(appUpdate ? "管理端" : "")可更新。")
                } else {
                    feedback.success("当前已是最新版本", detail: "XIASS API 服务端与管理端均无需更新。")
                }
            }
        } catch { self.error = ErrorMessage(error, title: "无法检查更新") }
    }

    private func performServerUpdate() {
        Task {
            isUpdating = true
            defer { isUpdating = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let result: UpdateResult = try await api.request(method: .post, path: "admin/system/update", body: EmptyResponse())
                feedback.notice("服务端更新已开始", detail: result.message)
                try? await Task.sleep(for: .seconds(6))
                await checkUpdates(notify: true)
            } catch { self.error = ErrorMessage(error, title: "服务端更新失败") }
        }
    }
}
