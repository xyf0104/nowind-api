import SwiftUI
import UIKit

struct AccountCreationHubView: View {
    @Environment(\.dismiss) private var dismiss
    let onSaved: () -> Void

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(PlatformOption.allCases) { option in
                        NavigationLink {
                            OAuthAccountFlow(platform: option, onSaved: onSaved)
                        } label: {
                            HStack(spacing: 12) {
                                GlassIcon(name: PlatformStyle.icon(for: option.rawValue), tint: PlatformStyle.color(for: option.rawValue))
                                VStack(alignment: .leading, spacing: 2) {
                                    Text("添加 \(option.title) OAuth 账号")
                                    Text("生成授权链接，授权后自动导入凭证")
                                        .font(.caption).foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                } header: {
                    Text("OAuth 授权添加")
                } footer: {
                    Text("OAuth 授权在系统安全浏览器中完成，账号密钥不会长期保存在手机内。")
                }

                Section("手动凭证") {
                    NavigationLink {
                        AccountEditorView(onSaved: onSaved)
                    } label: {
                        Label("手动添加 API Key、Refresh Token 或 JSON 凭证", systemImage: "key.fill")
                    }
                }
            }
            .appScreenStyle()
            .navigationTitle("添加上游账号")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("关闭") { dismiss() } } }
        }
    }
}

private struct OAuthStartRequest: Encodable {
    let redirectURI: String?
    let oauthType: String?
    let proxyID: Int?
    let projectID: String?
    let tierID: String?

    enum CodingKeys: String, CodingKey {
        case redirectURI = "redirect_uri"
        case oauthType = "oauth_type"
        case proxyID = "proxy_id"
        case projectID = "project_id"
        case tierID = "tier_id"
    }
}

private struct OAuthExchangeRequest: Encodable {
    let sessionID: String
    let code: String
    let state: String
    let redirectURI: String?
    let oauthType: String?
    let proxyID: Int?
    let tierID: String?

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case code, state
        case redirectURI = "redirect_uri"
        case oauthType = "oauth_type"
        case proxyID = "proxy_id"
        case tierID = "tier_id"
    }
}

private struct ApplyOAuthCredentialsRequest: Encodable {
    let type: String
    let credentials: [String: JSONValue]
    let extra: [String: JSONValue]?
}

private struct OAuthAuthorizationResult: Identifiable {
    let id = UUID()
    let account: AdminAccount
    let title: String
    let detail: String
}

private enum SMSReceiveOutcome {
    case waiting
    case received
    case expired
    case unavailable
}

@MainActor
private final class SMSReceiveAssistant: ObservableObject {
    enum Phase: Equatable {
        case idle
        case activating
        case waiting
        case received
        case expired
        case unavailable
        case failed
    }

    @Published private(set) var phase: Phase = .idle
    @Published private(set) var numberDisplay = "--"
    @Published private(set) var numberForCopy = ""
    @Published private(set) var countryCallingCode = ""
    @Published private(set) var localNumberDisplay = "--"
    @Published private(set) var regionDisplay = "--"
    @Published private(set) var countryFlag = ""
    @Published private(set) var codeDisplay = "--"
    @Published private(set) var queuedKeyCount = 0
    @Published private(set) var isRefreshing = false
    @Published private(set) var isChangingNumber = false
    @Published private(set) var isCancelling = false

    private var api: APIClient?
    private var activeSessionID: String?
    private var pollingTask: Task<Void, Never>?

    var statusText: String {
        switch phase {
        case .idle: return "等待取号"
        case .activating: return "正在取号"
        case .waiting: return "实时监听"
        case .received: return "接收成功"
        case .expired: return "已超时"
        case .unavailable: return "暂无卡密"
        case .failed: return "连接失败"
        }
    }

    var statusColor: Color {
        switch phase {
        case .received: return .green
        case .expired, .failed: return .red
        case .unavailable: return .orange
        case .waiting, .activating: return AppTheme.primary
        case .idle: return .secondary
        }
    }

    var canRefresh: Bool {
        activeSessionID != nil && !isRefreshing && !isChangingNumber && !isCancelling && phase != .activating && phase != .received && phase != .expired
    }

    var canChangeNumber: Bool {
        activeSessionID != nil && !isRefreshing && !isChangingNumber && !isCancelling && phase != .activating && phase != .received
    }

    var canCancel: Bool {
        activeSessionID != nil && !isRefreshing && !isChangingNumber && !isCancelling && phase != .activating && phase != .received
    }

    var needsPhoneRequest: Bool {
        [.idle, .unavailable, .expired, .failed].contains(phase)
    }

    func begin(using api: APIClient) async throws -> SMSReceiveOutcome {
        self.api = api
        if let sessionID = activeSessionID ?? SecureStore.activeSMSSessionID() {
            activeSessionID = sessionID
            return try await resume(sessionID: sessionID, using: api)
        }

        let queue = try await refreshQueueStatus(using: api)
        guard queue.queuedCount > 0 else {
            resetDisplay(phase: .unavailable)
            return .unavailable
        }

        phase = .activating
        numberDisplay = "正在获取…"
        regionDisplay = "--"
        codeDisplay = "--"
        do {
            return try adopt(try await api.redeemSMSReceiverNumber())
        } catch {
            phase = .failed
            throw error
        }
    }

    func refresh(using api: APIClient) async throws -> SMSReceiveOutcome {
        self.api = api
        guard let sessionID = activeSessionID ?? SecureStore.activeSMSSessionID() else {
            _ = try await refreshQueueStatus(using: api)
            resetDisplay(phase: .unavailable)
            return .unavailable
        }
        activeSessionID = sessionID
        isRefreshing = true
        defer { isRefreshing = false }

        do {
            let response = if phase == .failed || phase == .activating {
                try await api.resumeSMSReceiverNumber(sessionID: sessionID)
            } else {
                try await api.checkSMSReceiverNumber(sessionID: sessionID)
            }
            return try adopt(response)
        } catch {
            phase = .failed
            throw error
        }
    }

    func changeNumber(using api: APIClient) async throws -> SMSReceiveOutcome {
        self.api = api
        guard let sessionID = activeSessionID ?? SecureStore.activeSMSSessionID() else {
            throw APIError(message: "当前没有可更换的手机号。")
        }
        isChangingNumber = true
        stopPolling()
        defer {
            isChangingNumber = false
            if phase == .waiting { startPolling() }
        }

        do {
            return try adopt(try await api.changeSMSReceiverNumber(sessionID: sessionID))
        } catch {
            if phase == .waiting { startPolling() }
            throw error
        }
    }

    func cancel(using api: APIClient) async throws {
        self.api = api
        guard let sessionID = activeSessionID ?? SecureStore.activeSMSSessionID() else {
            resetDisplay(phase: .idle)
            return
        }
        isCancelling = true
        stopPolling()
        defer { isCancelling = false }

        do {
            let response = try await api.cancelSMSReceiverNumber(sessionID: sessionID)
            queuedKeyCount = response.queuedCount
            clearActiveSession()
            resetDisplay(phase: .idle)
        } catch {
            if phase == .waiting { startPolling() }
            throw error
        }
    }

    func resumePollingIfNeeded(using api: APIClient) {
        self.api = api
        guard phase == .waiting else { return }
        startPolling()
    }

    func stopPolling() {
        pollingTask?.cancel()
        pollingTask = nil
    }

    private func resume(sessionID: String, using api: APIClient) async throws -> SMSReceiveOutcome {
        phase = .activating
        do {
            return try adopt(try await api.resumeSMSReceiverNumber(sessionID: sessionID))
        } catch {
            phase = .failed
            throw error
        }
    }

    private func adopt(_ response: SMSReceiverSession) throws -> SMSReceiveOutcome {
        queuedKeyCount = response.queuedCount
        if let number = response.number?.trimmingCharacters(in: .whitespacesAndNewlines), !number.isEmpty {
            applyPhone(number, reportedRegion: response.country)
        }

        let status = response.status.trimmingCharacters(in: .whitespacesAndNewlines).uppercased()
        if let code = response.code?.trimmingCharacters(in: .whitespacesAndNewlines), !code.isEmpty, code != "--" {
            codeDisplay = code
            clearActiveSession()
            phase = .received
            return .received
        }

        if ["EXPIRED", "TIMEOUT", "CANCELLED", "CANCELED", "FAILED", "ERROR", "RECEIVED", "COMPLETED", "USED"].contains(status) {
            clearActiveSession()
            phase = .expired
            codeDisplay = "--"
            return .expired
        }

        guard let sessionID = response.sessionID?.trimmingCharacters(in: .whitespacesAndNewlines), !sessionID.isEmpty else {
            clearActiveSession()
            resetDisplay(phase: .unavailable)
            return .unavailable
        }

        activeSessionID = sessionID
        try SecureStore.saveActiveSMSSessionID(sessionID)
        phase = .waiting
        startPolling()
        return .waiting
    }

    private func startPolling() {
        stopPolling()
        guard activeSessionID != nil, let api else { return }
        pollingTask = Task { [weak self, api] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 5_000_000_000)
                guard !Task.isCancelled, let self, self.phase == .waiting else { return }
                _ = try? await self.refresh(using: api)
            }
        }
    }

    private func clearActiveSession() {
        stopPolling()
        activeSessionID = nil
        SecureStore.clearActiveSMSSessionID()
    }

    private func resetDisplay(phase: Phase) {
        stopPolling()
        self.phase = phase
        numberDisplay = phase == .unavailable ? "暂无待用卡密" : "--"
        numberForCopy = ""
        countryCallingCode = ""
        localNumberDisplay = "--"
        regionDisplay = "--"
        countryFlag = ""
        codeDisplay = "--"
    }

    @discardableResult
    private func refreshQueueStatus(using api: APIClient) async throws -> SMSReceiverQueueStatus {
        let status = try await api.smsReceiverStatus()
        queuedKeyCount = status.queuedCount
        return status
    }

    private func applyPhone(_ number: String, reportedRegion: String?) {
        let digits = number.filter(\.isNumber)
        guard !digits.isEmpty else { return }
        let trimmedReportedRegion = reportedRegion?.trimmingCharacters(in: .whitespacesAndNewlines)
        let suppliedRegion = trimmedReportedRegion?.isEmpty == false ? trimmedReportedRegion : nil

        if let callingCode = Self.callingCodes.first(where: { digits.hasPrefix($0.code) && digits.count > $0.code.count }) {
            numberDisplay = "+\(callingCode.code) \(digits.dropFirst(callingCode.code.count))"
            numberForCopy = String(digits.dropFirst(callingCode.code.count))
            countryCallingCode = "+\(callingCode.code)"
            localNumberDisplay = numberForCopy
            let country = Self.countryInfo(reportedRegion: suppliedRegion, fallbackRegion: callingCode.region, callingCode: callingCode.code)
            regionDisplay = country.name
            countryFlag = country.flag
        } else {
            numberDisplay = "+\(digits)"
            numberForCopy = digits
            countryCallingCode = ""
            localNumberDisplay = digits
            let country = Self.countryInfo(reportedRegion: suppliedRegion, fallbackRegion: "自动识别", callingCode: nil)
            regionDisplay = country.name
            countryFlag = country.flag
        }
    }

    private static func countryInfo(reportedRegion: String?, fallbackRegion: String, callingCode: String?) -> (name: String, flag: String) {
        let raw = reportedRegion?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let aliases: [String: (name: String, flag: String)] = [
            "us": ("美国", "🇺🇸"), "usa": ("美国", "🇺🇸"), "united states": ("美国", "🇺🇸"),
            "ca": ("加拿大", "🇨🇦"), "canada": ("加拿大", "🇨🇦"),
            "gb": ("英国", "🇬🇧"), "uk": ("英国", "🇬🇧"), "united kingdom": ("英国", "🇬🇧"),
            "jp": ("日本", "🇯🇵"), "japan": ("日本", "🇯🇵"),
            "kr": ("韩国", "🇰🇷"), "south korea": ("韩国", "🇰🇷"),
            "cn": ("中国", "🇨🇳"), "china": ("中国", "🇨🇳"),
            "hk": ("中国香港", "🇭🇰"), "hong kong": ("中国香港", "🇭🇰"),
            "mo": ("中国澳门", "🇲🇴"), "macau": ("中国澳门", "🇲🇴"),
            "tw": ("中国台湾", "🇹🇼"), "taiwan": ("中国台湾", "🇹🇼"),
            "sg": ("新加坡", "🇸🇬"), "singapore": ("新加坡", "🇸🇬"),
            "au": ("澳大利亚", "🇦🇺"), "australia": ("澳大利亚", "🇦🇺"),
            "my": ("马来西亚", "🇲🇾"), "malaysia": ("马来西亚", "🇲🇾"),
            "th": ("泰国", "🇹🇭"), "thailand": ("泰国", "🇹🇭"),
            "vn": ("越南", "🇻🇳"), "vietnam": ("越南", "🇻🇳"),
            "ph": ("菲律宾", "🇵🇭"), "philippines": ("菲律宾", "🇵🇭"),
            "id": ("印度尼西亚", "🇮🇩"), "indonesia": ("印度尼西亚", "🇮🇩"),
            "in": ("印度", "🇮🇳"), "india": ("印度", "🇮🇳"),
            "de": ("德国", "🇩🇪"), "germany": ("德国", "🇩🇪"),
            "fr": ("法国", "🇫🇷"), "france": ("法国", "🇫🇷"),
            "es": ("西班牙", "🇪🇸"), "spain": ("西班牙", "🇪🇸"),
            "it": ("意大利", "🇮🇹"), "italy": ("意大利", "🇮🇹"),
            "br": ("巴西", "🇧🇷"), "brazil": ("巴西", "🇧🇷"),
            "mx": ("墨西哥", "🇲🇽"), "mexico": ("墨西哥", "🇲🇽"),
            "ru": ("俄罗斯", "🇷🇺"), "russia": ("俄罗斯", "🇷🇺"),
            "tr": ("土耳其", "🇹🇷"), "turkey": ("土耳其", "🇹🇷"),
            "ae": ("阿拉伯联合酋长国", "🇦🇪"), "united arab emirates": ("阿拉伯联合酋长国", "🇦🇪")
        ]
        if let alias = aliases[raw.lowercased()] { return alias }
        let flags: [String: String] = [
            "1": "🇺🇸", "7": "🇷🇺", "20": "🇪🇬", "27": "🇿🇦", "30": "🇬🇷", "31": "🇳🇱", "32": "🇧🇪", "33": "🇫🇷", "34": "🇪🇸", "39": "🇮🇹", "44": "🇬🇧", "49": "🇩🇪", "52": "🇲🇽", "55": "🇧🇷", "60": "🇲🇾", "61": "🇦🇺", "62": "🇮🇩", "63": "🇵🇭", "65": "🇸🇬", "66": "🇹🇭", "81": "🇯🇵", "82": "🇰🇷", "84": "🇻🇳", "86": "🇨🇳", "90": "🇹🇷", "91": "🇮🇳", "852": "🇭🇰", "853": "🇲🇴", "886": "🇹🇼", "971": "🇦🇪"
        ]
        return (raw.isEmpty ? fallbackRegion : raw, callingCode.flatMap { flags[$0] } ?? "")
    }

    private static let callingCodes: [(code: String, region: String)] = [
        ("1", "美国/加拿大"), ("7", "俄罗斯/哈萨克斯坦"), ("20", "埃及"), ("27", "南非"),
        ("30", "希腊"), ("31", "荷兰"), ("32", "比利时"), ("33", "法国"),
        ("34", "西班牙"), ("39", "意大利"), ("44", "英国"), ("49", "德国"),
        ("52", "墨西哥"), ("55", "巴西"), ("60", "马来西亚"), ("61", "澳大利亚"),
        ("62", "印度尼西亚"), ("63", "菲律宾"), ("65", "新加坡"), ("66", "泰国"),
        ("81", "日本"), ("82", "韩国"), ("84", "越南"), ("86", "中国"),
        ("90", "土耳其"), ("91", "印度"), ("92", "巴基斯坦"), ("93", "阿富汗"),
        ("94", "斯里兰卡"), ("95", "缅甸"), ("98", "伊朗"), ("212", "摩洛哥"),
        ("351", "葡萄牙"), ("352", "卢森堡"), ("353", "爱尔兰"), ("354", "冰岛"),
        ("358", "芬兰"), ("380", "乌克兰"), ("420", "捷克"), ("852", "香港"),
        ("853", "澳门"), ("855", "柬埔寨"), ("856", "老挝"), ("880", "孟加拉国"),
        ("886", "台湾"), ("960", "马尔代夫"), ("961", "黎巴嫩"), ("971", "阿联酋")
    ].sorted { $0.code.count > $1.code.count }
}

private struct SMSReceiveAssistantCard: View {
    enum Confirmation: Identifiable {
        case changeNumber
        case cancel

        var id: String {
            switch self {
            case .changeNumber: return "change-number"
            case .cancel: return "cancel"
            }
        }
    }

    let api: APIClient
    @ObservedObject var receiver: SMSReceiveAssistant
    @EnvironmentObject private var feedback: FeedbackCenter
    @State private var confirmation: Confirmation?

    var body: some View {
        VStack(spacing: 12) {
            if receiver.needsPhoneRequest {
                Button {
                    Task { await requestPhone() }
                } label: {
                    Label(receiver.phase == .activating ? "正在获取手机号…" : "获取手机号", systemImage: "message.badge")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .disabled(receiver.phase == .activating)
            } else {
                HStack(alignment: .firstTextBaseline, spacing: 12) {
                field("手机号") {
                    HStack(spacing: 7) {
                        if !receiver.countryCallingCode.isEmpty {
                            Text(receiver.countryCallingCode)
                                .font(.caption.monospacedDigit().weight(.semibold))
                                .foregroundStyle(.secondary)
                                .padding(.horizontal, 6)
                                .padding(.vertical, 3)
                                .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 5, style: .continuous))
                        }
                        Button {
                            copy(receiver.numberForCopy, title: "手机号已复制", detail: "已复制不含国家区号的号码，可直接粘贴。")
                        } label: {
                            HStack(spacing: 5) {
                                Text(receiver.localNumberDisplay)
                                if !receiver.numberForCopy.isEmpty {
                                    Image(systemName: "doc.on.doc")
                                        .font(.caption.weight(.semibold))
                                }
                            }
                            .font(.subheadline.monospacedDigit().weight(.semibold))
                            .foregroundStyle(receiver.numberForCopy.isEmpty ? .secondary : .primary)
                            .lineLimit(1)
                            .minimumScaleFactor(0.72)
                        }
                        .buttonStyle(.plain)
                        .disabled(receiver.numberForCopy.isEmpty)
                    }
                }

                Spacer(minLength: 4)

                field("地区") {
                    HStack(spacing: 5) {
                        if !receiver.countryFlag.isEmpty { Text(receiver.countryFlag) }
                        Text(receiver.regionDisplay)
                    }
                        .font(.subheadline.weight(.medium))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }

                StatusPill(text: receiver.statusText)
                    .foregroundStyle(receiver.statusColor)
                }

                Divider()

                HStack(spacing: 12) {
                field("验证码") {
                    Button {
                        copy(receiver.codeDisplay, title: "验证码已复制", detail: "可直接粘贴到 OpenAI 验证页面。")
                    } label: {
                        HStack(spacing: 5) {
                            Text(receiver.codeDisplay)
                            if receiver.phase == .received {
                                Image(systemName: "doc.on.doc")
                                    .font(.caption.weight(.semibold))
                            }
                        }
                        .font(.system(.title3, design: .rounded, weight: .bold).monospacedDigit())
                        .foregroundStyle(receiver.phase == .received ? .green : .primary)
                    }
                    .buttonStyle(.plain)
                    .disabled(receiver.phase != .received)
                }

                Spacer(minLength: 8)

                Button {
                    Task { await refresh() }
                } label: {
                    Label(receiver.isRefreshing ? "刷新中" : "刷新", systemImage: "arrow.clockwise")
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(!receiver.canRefresh)

                Button { confirmation = .changeNumber } label: {
                    Label(receiver.isChangingNumber ? "换号中" : "换号", systemImage: "arrow.triangle.2.circlepath")
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(!receiver.canChangeNumber)

                Button(role: .destructive) { confirmation = .cancel } label: {
                    Label(receiver.isCancelling ? "取消中" : "取消", systemImage: "xmark.circle")
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(!receiver.canCancel)
                }
            }
        }
        .padding(.vertical, 6)
        .alert(item: $confirmation) { action in
            switch action {
            case .changeNumber:
                Alert(
                    title: Text("更换手机号？"),
                    message: Text("当前手机号会被取消，卡密会保留在 XIASS API 服务器队列中，并自动重新取号。"),
                    primaryButton: .destructive(Text("确认换号")) { Task { await changeNumber() } },
                    secondaryButton: .cancel(Text("返回"))
                )
            case .cancel:
                Alert(
                    title: Text("取消当前取号？"),
                    message: Text("当前接码会话会被取消；卡密不会作废，只有实际收到验证码后才会清除。"),
                    primaryButton: .destructive(Text("确认取消")) { Task { await cancel() } },
                    secondaryButton: .cancel(Text("返回"))
                )
            }
        }
    }

    private func field<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            content()
        }
    }

    private func copy(_ value: String, title: String, detail: String) {
        guard !value.isEmpty, value != "--" else { return }
        UIPasteboard.general.string = value
        feedback.success(title, detail: detail)
    }

    private func requestPhone() async {
        do {
            let outcome = try await receiver.begin(using: api)
            switch outcome {
            case .waiting:
                feedback.success("手机号已获取", detail: "点击本地号码即可复制，系统会继续监听验证码。")
            case .received:
                feedback.success("已收到验证码", detail: "点击验证码即可复制。")
            case .expired:
                feedback.notice("取号已超时", detail: "卡密已返回服务器待用队列，可重新获取手机号。")
            case .unavailable:
                feedback.notice("暂无可用卡密", detail: "请在设置的“接码卡密”中加入新的卡密。")
            }
        } catch {
            feedback.failure(error, title: "获取手机号失败")
        }
    }

    private func refresh() async {
        do {
            let outcome = try await receiver.refresh(using: api)
            switch outcome {
            case .waiting:
                feedback.notice("接码状态已刷新", detail: "暂未收到验证码，后台仍在实时监听。")
            case .received:
                feedback.success("已收到验证码", detail: "点击验证码即可复制。")
            case .expired:
                feedback.notice("取号已超时", detail: "该会话已结束，卡密已保留在服务器待用队列中。")
            case .unavailable:
                feedback.notice("暂无可用卡密", detail: "请在设置的“接码卡密”中批量加入新的卡密。")
            }
        } catch {
            feedback.failure(error, title: "刷新接码状态失败")
        }
    }

    private func changeNumber() async {
        do {
            let outcome = try await receiver.changeNumber(using: api)
            switch outcome {
            case .waiting:
                feedback.success("已更换手机号", detail: "新号码正在实时监听验证码。")
            case .received:
                feedback.success("已更换手机号", detail: "新号码已收到验证码，点击即可复制。")
            case .expired:
                feedback.notice("新号码已超时", detail: "请重新生成授权链接后继续。")
            case .unavailable:
                feedback.notice("暂无可用卡密", detail: "当前号码已取消，请先补充服务器待用卡密。")
            }
        } catch {
            feedback.failure(error, title: "更换手机号失败")
        }
    }

    private func cancel() async {
        do {
            try await receiver.cancel(using: api)
            feedback.success("已取消当前取号", detail: "当前会话已清理，卡密已返回服务器待用队列。")
        } catch {
            feedback.failure(error, title: "取消取号失败")
        }
    }
}

struct OAuthAccountFlow: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase

    let platform: PlatformOption
    let existingAccount: AdminAccount?
    let onSaved: () -> Void

    @State private var name = ""
    @State private var concurrency = 1
    @State private var priority = 0
    @State private var rateMultiplier = 1.0
    @State private var groups: [AdminGroup] = []
    @State private var selectedGroupIDs = Set<Int>()
    @State private var sessionID = ""
    @State private var state = ""
    @State private var authorizationURL: URL?
    @State private var authorizationCode = ""
    @State private var callbackText = ""
    @State private var isAuthorizing = false
    @State private var isOpeningBrowser = false
    @State private var isSaving = false
    @State private var isAwaitingBrowserCallback = false
    @State private var lastClipboardCallback = ""
    @State private var authorizationResult: OAuthAuthorizationResult?
    @State private var editorAccount: AdminAccount?
    @State private var error: ErrorMessage?
    @StateObject private var smsReceiver = SMSReceiveAssistant()

    init(platform: PlatformOption, existingAccount: AdminAccount? = nil, onSaved: @escaping () -> Void) {
        self.platform = platform
        self.existingAccount = existingAccount
        self.onSaved = onSaved
    }

    private var isReauthorization: Bool { existingAccount != nil }
    private var sortedGroups: [AdminGroup] { groups.filter { $0.platform == platform.rawValue }.sorted { ($0.rateMultiplier ?? 1) < ($1.rateMultiplier ?? 1) } }

    var body: some View {
        Form {
            if let existingAccount {
                Section {
                    HStack(spacing: 10) {
                        GlassIcon(name: PlatformStyle.icon(for: platform.rawValue), tint: PlatformStyle.color(for: platform.rawValue))
                        VStack(alignment: .leading, spacing: 2) {
                            Text(existingAccount.name)
                            Text("\(platform.title) OAuth · 账号 ID \(existingAccount.id)")
                                .font(.caption).foregroundStyle(.secondary)
                            if let email = existingAccount.displayEmail {
                                Text(email)
                                    .font(.caption).foregroundStyle(.secondary)
                                    .lineLimit(1)
                            }
                        }
                    }
                    LabeledContent("当前优先级", value: String(existingAccount.priority ?? 0))
                    LabeledContent("绑定分组", value: existingAccount.groupIDs?.isEmpty == false ? "\(existingAccount.groupIDs?.count ?? 0) 个" : "未分组")
                } header: {
                    Text("重新授权账号")
                } footer: {
                    Text("重新授权只更新上游凭证并清除账号错误；名称、分组、并发、优先级、倍率和其他运行设置都会保留。")
                }
            } else {
                Section("账号设置") {
                    HStack(spacing: 10) {
                        GlassIcon(name: PlatformStyle.icon(for: platform.rawValue), tint: PlatformStyle.color(for: platform.rawValue))
                        VStack(alignment: .leading, spacing: 2) {
                            Text("\(platform.title) OAuth")
                            Text("授权完成后自动创建上游账号").font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    TextField("账号名称（留空自动使用邮箱）", text: $name)
                    IntegerInput(label: "并发数", value: $concurrency, range: 1...100, step: 1)
                    IntegerInput(label: "优先级", value: $priority, range: 0...100, step: 1)
                    DecimalInput(label: "费率倍率", value: $rateMultiplier, range: 0...100, step: 0.05, suffix: "x")
                }

                Section("绑定分组") {
                    if sortedGroups.isEmpty {
                        Text("暂无 \(platform.title) 分组，授权后仍可在账号详情中绑定。")
                            .font(.footnote).foregroundStyle(.secondary)
                    } else {
                        ForEach(sortedGroups) { group in
                            Toggle(isOn: Binding(get: { selectedGroupIDs.contains(group.id) }, set: { enabled in
                                if enabled { selectedGroupIDs.insert(group.id) } else { selectedGroupIDs.remove(group.id) }
                            })) {
                                HStack {
                                    Text(group.name)
                                    Spacer()
                                    Text("\(DisplayFormat.decimal(group.rateMultiplier ?? 1))x")
                                        .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                }
            }

            Section("授权") {
                Button(isAuthorizing ? "正在生成授权链接…" : isReauthorization ? "生成重新授权链接" : "生成授权链接") { beginAuthorization() }
                    .disabled(isAuthorizing || isOpeningBrowser || isSaving)

                if let authorizationURL {
                    OAuthFlowStep(number: 1, title: "打开授权链接") {
                        Text(authorizationURL.absoluteString)
                            .font(.caption.monospaced())
                            .textSelection(.enabled)
                            .lineLimit(3...6)
                            .foregroundStyle(.secondary)

                        HStack(spacing: 12) {
                            Button {
                                UIPasteboard.general.string = authorizationURL.absoluteString
                                feedback.success("授权链接已复制", detail: "可粘贴到任意浏览器完成登录。")
                            } label: {
                                Label("复制链接", systemImage: "doc.on.doc")
                            }

                            Button(isOpeningBrowser ? "正在打开…" : "打开浏览器") {
                                openAuthorizationURL(authorizationURL)
                            }
                            .disabled(isOpeningBrowser || isSaving)
                        }
                    }

                    OAuthFlowStep(number: 2, title: "完成网页登录") {
                        Text("登录完成后返回此页；已复制的回调链接会自动识别。也可手动粘贴浏览器最终地址或授权码。")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }

                    if usesSMSReceiver, let api = session.api {
                        SMSReceiveAssistantCard(api: api, receiver: smsReceiver)
                    }

                    OAuthFlowStep(number: 3, title: "导入授权凭证") {
                        TextField("粘贴回调链接或授权码", text: $callbackText, axis: .vertical)
                            .lineLimit(2...4)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()

                        HStack(spacing: 12) {
                            Button {
                                guard let value = UIPasteboard.general.string?.trimmingCharacters(in: .whitespacesAndNewlines), !value.isEmpty else {
                                    feedback.notice("剪贴板没有可导入的内容", detail: "请先复制回调链接或授权码。")
                                    return
                                }
                                callbackText = value
                                importManualCallback()
                            } label: {
                                Label("从剪贴板导入", systemImage: "clipboard")
                            }
                            .disabled(isSaving)

                            Button(isSaving ? "正在导入…" : "完成授权") { importManualCallback() }
                                .disabled(isSaving || callbackText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        }
                    }

                    Button {
                        beginAuthorization()
                    } label: {
                        Label("重新生成链接", systemImage: "arrow.clockwise")
                    }
                    .disabled(isAuthorizing || isOpeningBrowser || isSaving)
                }
            }
        }
        .scrollDismissesKeyboard(.interactively)
        .appScreenStyle()
        .navigationTitle(isReauthorization ? "重新授权" : "添加 \(platform.title)")
        .navigationBarTitleDisplayMode(.inline)
        .task { await loadGroups() }
        .onOpenURL { receive($0) }
        .onChange(of: scenePhase) { phase in
            guard phase == .active else { return }
            importClipboardCallbackIfAvailable()
            if let api = session.api {
                smsReceiver.resumePollingIfNeeded(using: api)
            }
        }
        .onDisappear { smsReceiver.stopPolling() }
        .alert(item: $authorizationResult) { result in
            Alert(
                title: Text(result.title),
                message: Text(result.detail),
                primaryButton: .default(Text("下一步")) {
                    editorAccount = result.account
                },
                secondaryButton: .cancel(Text("完成")) {
                    dismiss()
                }
            )
        }
        .sheet(item: $editorAccount) { account in
            AccountEditorView(account: account, onSaved: onSaved)
        }
        .requestError($error)
    }

    private func loadGroups() async {
        guard let api = session.api else { return }
        do { groups = try await api.request(method: .get, path: "admin/groups/all", query: [URLQueryItem(name: "include_inactive", value: "true")]) }
        catch { self.error = ErrorMessage(error, title: "无法读取分组") }
    }

    private func beginAuthorization() {
        Task {
            isAuthorizing = true
            defer { isAuthorizing = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                // Reuse the callback URI generated by XIASS API's web flow.
                // OpenAI's Codex OAuth client rejects an unregistered custom
                // scheme such as xiassadmin://oauth before the user can sign in.
                let request = OAuthStartRequest(
                    redirectURI: nil,
                    oauthType: oauthType,
                    proxyID: existingAccount?.proxyID,
                    projectID: geminiProjectID,
                    tierID: geminiTierID
                )
                let auth: OAuthAuthorization = try await api.request(method: .post, path: authorizationPath, body: request)
                sessionID = auth.sessionID
                state = auth.state ?? ""
                authorizationCode = ""
                callbackText = ""
                lastClipboardCallback = ""
                isAwaitingBrowserCallback = false
                guard let url = URL(string: auth.authURL) else { throw APIError(message: "服务端返回的授权链接无效。") }
                authorizationURL = url
                feedback.notice(isReauthorization ? "重新授权链接已生成" : "授权链接已生成", detail: "复制链接或点击“打开浏览器”后完成 \(platform.title) 登录。")
            } catch { self.error = ErrorMessage(error, title: "生成授权链接失败") }
        }
    }

    private func openAuthorizationURL(_ url: URL) {
        isOpeningBrowser = true
        UIApplication.shared.open(url, options: [:]) { opened in
            DispatchQueue.main.async {
                isOpeningBrowser = false
                if opened {
                    isAwaitingBrowserCallback = true
                    feedback.notice("已在浏览器打开授权页", detail: "完成登录后回到此页，已复制的回调链接会自动导入。")
                } else {
                    error = ErrorMessage(APIError(message: "无法打开系统浏览器。"), title: "打开授权页失败")
                }
            }
        }
    }

    private func receive(_ url: URL) {
        let components = URLComponents(url: url, resolvingAgainstBaseURL: false)
        if let providerError = components?.queryItems?.first(where: { $0.name == "error" })?.value {
            let description = components?.queryItems?.first(where: { $0.name == "error_description" })?.value
            isAwaitingBrowserCallback = false
            error = ErrorMessage(APIError(message: description ?? providerError), title: "OAuth 授权失败")
            return
        }
        authorizationCode = components?.queryItems?.first(where: { $0.name == "code" })?.value ?? ""
        state = components?.queryItems?.first(where: { $0.name == "state" })?.value ?? state
        callbackText = url.absoluteString
        guard !authorizationCode.isEmpty else {
            feedback.notice("未识别授权码", detail: "请将授权页最后的回调链接粘贴到本页。")
            return
        }
        Task { await exchangeAndCreate() }
    }

    private func importManualCallback() {
        let input = callbackText.trimmingCharacters(in: .whitespacesAndNewlines)
        if let url = callbackURL(from: input) {
            receive(url)
            return
        }
        authorizationCode = input
        Task { await exchangeAndCreate() }
    }

    private func exchangeAndCreate() async {
        guard !sessionID.isEmpty, !authorizationCode.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            error = ErrorMessage(APIError(message: "请先生成授权链接并完成登录授权。"), title: "无法导入凭证")
            return
        }
        isSaving = true
        defer { isSaving = false }
        do {
            guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
            let body = OAuthExchangeRequest(
                sessionID: sessionID,
                code: authorizationCode.trimmingCharacters(in: .whitespacesAndNewlines),
                state: state,
                redirectURI: nil,
                oauthType: oauthType,
                proxyID: existingAccount?.proxyID,
                tierID: geminiTierID
            )
            let token: OAuthTokenInfo = try await api.request(method: .post, path: exchangePath, body: body)
            guard !token.credentials.isEmpty else { throw APIError(message: "授权成功但没有返回可保存的凭证。") }
            let savedAccount: AdminAccount
            let title: String
            let detail: String
            if let existingAccount {
                let request = ApplyOAuthCredentialsRequest(
                    type: existingAccount.type == "setup-token" ? "setup-token" : "oauth",
                    credentials: credentialsForPersistence(token),
                    extra: extraForPersistence(token)
                )
                savedAccount = try await api.request(method: .post, path: "admin/accounts/\(existingAccount.id)/apply-oauth-credentials", body: request)
                title = "OAuth 授权成功"
                detail = "\(savedAccount.name) 的上游凭证已更新。点击“下一步”可继续编辑账号配置。"
            } else {
                let accountName = name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? (token.email ?? "\(platform.title) OAuth 账号") : name.trimmingCharacters(in: .whitespacesAndNewlines)
                let request = AccountCreateRequest(name: accountName, notes: "通过 XIASS 管理端 OAuth 导入", platform: platform.rawValue, type: "oauth", credentials: credentialsForPersistence(token), extra: extraForPersistence(token), concurrency: concurrency, priority: priority, rateMultiplier: rateMultiplier, loadFactor: nil, groupIDs: Array(selectedGroupIDs).sorted(), confirmMixedChannelRisk: true)
                savedAccount = try await api.request(method: .post, path: "admin/accounts", body: request)
                title = "OAuth 授权成功"
                detail = "\(savedAccount.name) 已创建并加入调度。点击“下一步”可继续编辑账号配置。"
            }
            isAwaitingBrowserCallback = false
            onSaved()
            authorizationResult = OAuthAuthorizationResult(account: savedAccount, title: title, detail: detail)
        } catch { self.error = ErrorMessage(error, title: "OAuth 授权失败") }
    }

    private func importClipboardCallbackIfAvailable() {
        guard isAwaitingBrowserCallback, !isSaving else { return }
        guard let clipboard = UIPasteboard.general.string?.trimmingCharacters(in: .whitespacesAndNewlines),
              let callbackURL = callbackURL(from: clipboard),
              clipboard != lastClipboardCallback else { return }

        lastClipboardCallback = clipboard
        callbackText = clipboard
        feedback.notice("已识别授权回调", detail: "正在验证并导入上游 OAuth 凭证。")
        receive(callbackURL)
    }

    private func callbackURL(from input: String) -> URL? {
        guard let url = URL(string: input),
              let components = URLComponents(url: url, resolvingAgainstBaseURL: false),
              let items = components.queryItems,
              items.contains(where: { $0.name == "code" || $0.name == "error" }) else { return nil }
        return url
    }

    private var authorizationPath: String {
        switch platform {
        case .openai: return "admin/openai/generate-auth-url"
        case .anthropic: return existingAccount?.type == "setup-token" ? "admin/accounts/generate-setup-token-url" : "admin/accounts/generate-auth-url"
        case .gemini: return "admin/gemini/oauth/auth-url"
        case .antigravity: return "admin/antigravity/oauth/auth-url"
        case .grok: return "admin/grok/oauth/auth-url"
        }
    }

    private var exchangePath: String {
        switch platform {
        case .openai: return "admin/openai/exchange-code"
        case .anthropic: return existingAccount?.type == "setup-token" ? "admin/accounts/exchange-setup-token-code" : "admin/accounts/exchange-code"
        case .gemini: return "admin/gemini/oauth/exchange-code"
        case .antigravity: return "admin/antigravity/oauth/exchange-code"
        case .grok: return "admin/grok/oauth/exchange-code"
        }
    }

    private var oauthType: String? {
        guard platform == .gemini else { return nil }
        let configured = existingAccount?.credentials?["oauth_type"]?.stringValue
        return ["code_assist", "google_one", "ai_studio"].contains(configured ?? "") ? configured : "code_assist"
    }

    private var usesSMSReceiver: Bool { platform == .openai }

    private var geminiProjectID: String? {
        guard oauthType == "code_assist" else { return nil }
        return existingAccount?.credentials?["project_id"]?.stringValue
    }

    private var geminiTierID: String? {
        existingAccount?.credentials?["tier_id"]?.stringValue
    }

    private func credentialsForPersistence(_ token: OAuthTokenInfo) -> [String: JSONValue] {
        let sensitiveKeys: Set<String> = [
            "access_token", "refresh_token", "id_token", "agent_private_key", "api_key",
            "session_key", "cookie", "aws_secret_access_key", "aws_session_token",
            "service_account_json", "service_account", "private_key"
        ]
        var credentials = (existingAccount?.credentials ?? [:]).filter { !sensitiveKeys.contains($0.key) }
        token.credentials.forEach { credentials[$0.key] = $0.value }
        if platform == .grok, credentials["base_url"] == nil {
            credentials["base_url"] = .string("https://cli-chat-proxy.grok.com/v1")
        }
        return credentials
    }

    private func extraForPersistence(_ token: OAuthTokenInfo) -> [String: JSONValue]? {
        var extra = token.extra ?? [:]
        if let name = token.name, !name.isEmpty { extra["name"] = .string(name) }
        if let email = token.email, !email.isEmpty { extra["email"] = .string(email) }
        return extra.isEmpty ? nil : extra
    }
}

private struct OAuthFlowStep<Content: View>: View {
    let number: Int
    let title: String
    @ViewBuilder let content: Content

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Text("\(number)")
                .font(.caption.bold())
                .foregroundStyle(.white)
                .frame(width: 24, height: 24)
                .background(AppTheme.primary, in: Circle())
            VStack(alignment: .leading, spacing: 10) {
                Text(title).font(.subheadline.weight(.semibold))
                content
            }
        }
        .padding(.vertical, 4)
    }
}

struct ModelSelectionSheet: View {
    @Environment(\.dismiss) private var dismiss
    let models: [AccountTestModel]
    @Binding var selectedModel: String
    @State private var search = ""

    private var filteredModels: [AccountTestModel] {
        guard !search.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return models }
        return models.filter { $0.label.localizedCaseInsensitiveContains(search) || $0.id.localizedCaseInsensitiveContains(search) }
    }

    var body: some View {
        NavigationStack {
            List {
                Button { selectedModel = ""; dismiss() } label: {
                    HStack { Text("自动选择（推荐）"); Spacer(); if selectedModel.isEmpty { Image(systemName: "checkmark").foregroundStyle(AppTheme.primary) } }
                }
                ForEach(filteredModels) { item in
                    Button { selectedModel = item.id; dismiss() } label: {
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(item.displayName ?? item.id)
                                if item.displayName != nil && item.displayName != item.id { Text(item.id).font(.caption).foregroundStyle(.secondary) }
                            }
                            Spacer()
                            if selectedModel == item.id { Image(systemName: "checkmark").foregroundStyle(AppTheme.primary) }
                        }
                    }
                }
                if filteredModels.isEmpty { Text("没有匹配的模型").foregroundStyle(.secondary) }
            }
            .searchable(text: $search, prompt: "搜索模型")
            .appScreenStyle()
            .navigationTitle("选择测试模型")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } } }
        }
    }
}
