import SwiftUI

struct UsageLogsView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter

    @State private var logs: [UsageLog] = []
    @State private var isLoading = false
    @State private var search = ""
    @State private var error: ErrorMessage?

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                if isLoading && logs.isEmpty {
                    ProgressView("正在读取最近调用…").frame(maxWidth: .infinity, minHeight: 180)
                }
                if !isLoading && logs.isEmpty {
                    EmptyState(title: "暂无调用记录", systemImage: "clock.arrow.circlepath", detail: "新的 API 调用会实时显示在这里。")
                }
                ForEach(logs, id: \.stableID) { log in
                    NavigationLink { UsageLogDetailView(log: log) } label: {
                        UsageLogCard(log: log)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(16)
            .padding(.bottom, 100)
        }
        .appScreenStyle()
        .navigationTitle("最近调用")
        .navigationBarTitleDisplayMode(.inline)
        .searchable(text: $search, prompt: "搜索用户、模型或请求 ID")
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { Task { await load(notify: true) } } label: { Image(systemName: "arrow.clockwise") }
            }
        }
        .task(id: search) {
            do { try await Task.sleep(for: .milliseconds(250)) }
            catch { return }
            guard !Task.isCancelled else { return }
            await load()
        }
        .refreshable { await load(notify: true) }
        .requestError($error)
    }

    private func load(notify: Bool = false) async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            var query = [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "100")]
            if !search.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                query.append(URLQueryItem(name: "search", value: search))
            }
            let page: Page<UsageLog> = try await api.request(method: .get, path: "admin/usage", query: query)
            guard !Task.isCancelled else { return }
            logs = page.items
            if notify { feedback.success("调用记录已刷新", detail: "已加载 \(logs.count) 条最近调用。") }
        } catch {
            // Replacing a search query cancels the obsolete request.
            guard !Task.isCancelled, !isExpectedCancellation(error) else { return }
            self.error = ErrorMessage(error, title: "无法读取调用记录")
        }
    }
}

private struct UsageLogCard: View {
    let log: UsageLog

    var body: some View {
        GlassCard(tint: log.statusCode.map { (200..<300).contains($0) ? .green : .red } ?? .purple) {
            VStack(alignment: .leading, spacing: 7) {
                HStack(spacing: 8) {
                    PlatformBadge(platform: log.group?.platform ?? "")
                    Text(log.model ?? "未知模型")
                        .font(.headline)
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    Text(log.statusCode.map(String.init) ?? "--")
                        .font(.caption.monospacedDigit().weight(.bold))
                        .foregroundStyle((log.statusCode ?? 200) < 300 ? .green : .red)
                }
                Text(log.displayUser)
                    .font(.subheadline.weight(.medium))
                    .lineLimit(1)
                HStack(spacing: 8) {
                    Label(log.displayGroup, systemImage: "square.stack.3d.up")
                    Label(log.displayAccount, systemImage: "person.crop.rectangle.stack")
                }
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                HStack(spacing: 10) {
                    Text("推理 \(reasoningTitle(log.reasoningEffort))")
                    Text("首字 \(log.firstTokenMS.map { "\($0) ms" } ?? "--")")
                    Text("总延迟 \(log.durationMS.map { "\($0) ms" } ?? "--")")
                }
                .font(.caption2)
                .foregroundStyle(.secondary)
                Text(DisplayFormat.shortDate(log.createdAt))
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
        }
    }
}

struct UsageLogDetailView: View {
    let log: UsageLog

    var body: some View {
        List {
            Section("调用") {
                LabeledContent("用户", value: log.displayUser)
                LabeledContent("请求模型", value: log.model ?? "--")
                if let upstream = log.upstreamModel, !upstream.isEmpty { LabeledContent("上游模型", value: upstream) }
                LabeledContent("分组", value: log.displayGroup)
                LabeledContent("上游账号", value: log.displayAccount)
                LabeledContent("推理程度", value: reasoningTitle(log.reasoningEffort))
                LabeledContent("服务层级", value: log.serviceTier ?? "默认")
            }
            Section("链路与延迟") {
                LabeledContent("入口", value: log.inboundEndpoint ?? "--")
                LabeledContent("上游链路", value: log.upstreamEndpoint ?? "--")
                LabeledContent("模型映射", value: log.modelMappingChain ?? "无")
                LabeledContent("首字延迟", value: log.firstTokenMS.map { "\($0) ms" } ?? "--")
                LabeledContent("总延迟", value: log.durationMS.map { "\($0) ms" } ?? "--")
                LabeledContent("流式响应", value: log.stream == true ? "是" : "否")
                LabeledContent("请求类型", value: log.requestType ?? "--")
            }
            Section("计费与状态") {
                LabeledContent("状态码", value: log.statusCode.map(String.init) ?? "--")
                LabeledContent("实际成本", value: DisplayFormat.currency(log.actualCost))
                LabeledContent("Token", value: DisplayFormat.integer(log.totalTokens))
                LabeledContent("请求 ID", value: log.requestID ?? "--")
                LabeledContent("发生时间", value: DisplayFormat.shortDate(log.createdAt))
            }
        }
        .appScreenStyle()
        .navigationTitle("调用详情")
        .navigationBarTitleDisplayMode(.inline)
    }
}

func reasoningTitle(_ value: String?) -> String {
    switch value?.lowercased() {
    case "low": return "低"
    case "medium": return "中"
    case "high": return "高"
    case "xhigh", "max": return "超高"
    default: return value?.isEmpty == false ? value! : "默认"
    }
}
