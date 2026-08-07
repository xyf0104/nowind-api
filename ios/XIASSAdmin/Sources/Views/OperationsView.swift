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

            Section("授权辅助") {
                NavigationLink { SMSCardKeyQueueView() } label: {
                    GlassActionRow("接码卡密", detail: "服务器加密队列，OpenAI 授权时自动取号", icon: "number.square.fill", tint: .mint)
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

struct SMSCardKeyQueueView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @State private var pastedKeys = ""
    @State private var status: SMSReceiverQueueStatus?
    @State private var isLoading = false
    @State private var isAdding = false
    @State private var isClearing = false
    @State private var showClearConfirmation = false
    @State private var error: ErrorMessage?

    var body: some View {
        List {
            Section {
                LabeledContent("服务器待用卡密", value: "\(status?.queuedCount ?? 0) 张")
                LabeledContent("进行中的号码", value: "\(status?.activeCount ?? 0) 个")
            } header: {
                Text("XIASS API 接码队列")
            } footer: {
                Text("原始卡密仅加密保存于 XIASS API 服务器，手机只保存不透明会话标识。只有实际收到验证码时，服务端才会彻底删除对应卡密。")
            }

            Section {
                TextEditor(text: $pastedKeys)
                    .textInputAutocapitalization(.characters)
                    .autocorrectionDisabled()
                    .font(.body.monospaced())
                    .frame(minHeight: 132)
                    .padding(8)
                    .background(.thinMaterial, in: RoundedRectangle(cornerRadius: AppTheme.compactRadius, style: .continuous))
                    .overlay {
                        RoundedRectangle(cornerRadius: AppTheme.compactRadius, style: .continuous)
                            .stroke(.secondary.opacity(0.24), lineWidth: 0.8)
                    }

                Button {
                    Task { await addKeys() }
                } label: {
                    Label(isAdding ? "正在加密保存…" : "加入服务器队列", systemImage: "plus.circle.fill")
                }
                .disabled(isAdding || pastedKeys.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            } header: {
                Text("批量加入")
            } footer: {
                Text("可一次粘贴多张卡密，支持换行、空格、逗号或分号分隔。重复卡密会自动跳过；提交后会立即加密，原文不会回显、不会保存在手机或请求审计日志中。")
            }

            if (status?.queuedCount ?? 0) > 0 {
                Section {
                    Button(role: .destructive) { showClearConfirmation = true } label: {
                        Label(isClearing ? "正在清空…" : "清空服务器待用卡密", systemImage: "trash")
                    }
                    .disabled(isClearing)
                } footer: {
                    Text("只清除尚未使用的待用队列，不会中断当前正在接收验证码的手机号。")
                }
            }
        }
        .appScreenStyle()
        .navigationTitle("接码卡密")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    Task { await reloadStatus() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .disabled(isLoading)
                .accessibilityLabel("刷新接码队列")
            }
        }
        .task { await reloadStatus() }
        .alert("清空待用卡密？", isPresented: $showClearConfirmation) {
            Button("清空", role: .destructive) { Task { await clearQueuedKeys() } }
            Button("取消", role: .cancel) {}
        } message: {
            Text("仅清除服务器中尚未取号的卡密，当前接码会话不会受影响。已收到验证码的卡密不会保留。")
        }
        .requestError($error)
    }

    private func addKeys() async {
        guard let api = session.api else {
            error = ErrorMessage(APIError(message: "登录已失效，请重新登录。"), title: "保存接码卡密失败")
            return
        }
        isAdding = true
        defer { isAdding = false }
        do {
            let result = try await api.addSMSReceiverCardKeys(pastedKeys)
            pastedKeys = ""
            status = try await api.smsReceiverStatus()
            if result.addedCount == 0 {
                feedback.notice("没有新增卡密", detail: "粘贴内容为空，或其中的卡密已经在待用队列中。")
            } else {
                feedback.success("卡密已加入服务器队列", detail: "已加密保存 \(result.addedCount) 张；OpenAI 授权时会自动取号。")
            }
        } catch {
            self.error = ErrorMessage(error, title: "保存接码卡密失败")
        }
    }

    private func clearQueuedKeys() async {
        guard let api = session.api else {
            error = ErrorMessage(APIError(message: "登录已失效，请重新登录。"), title: "清空接码卡密失败")
            return
        }
        isClearing = true
        defer { isClearing = false }
        do {
            let result = try await api.clearSMSReceiverCardKeys()
            status = try await api.smsReceiverStatus()
            feedback.success("服务器待用卡密已清空", detail: "已清除 \(result.deletedCount) 张；当前接码会话仍可继续刷新、换号或取消。")
        } catch {
            self.error = ErrorMessage(error, title: "清空接码卡密失败")
        }
    }

    private func reloadStatus() async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            status = try await api.smsReceiverStatus()
        } catch {
            self.error = ErrorMessage(error, title: "读取接码队列失败")
        }
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
