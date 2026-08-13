import SwiftUI

struct RootView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter

    var body: some View {
        AppScreen {
            switch session.phase {
            case .restoring:
                ProgressView("正在连接 XIASS API")
                    .tint(AppTheme.primary)
                    .task { await session.restore() }
            case .signedOut:
                LoginView()
            case .signedIn:
                AdminTabView()
            }
        }
        .alert(item: $feedback.message) { message in
            Alert(
                title: Text(message.title),
                message: Text(message.detail),
                dismissButton: .default(Text("知道了"))
            )
        }
    }
}

struct AdminTabView: View {
    @State private var selection = 0

    var body: some View {
        TabView(selection: $selection) {
            NavigationStack { DashboardView() }
                .tabItem { Label("概览", systemImage: "rectangle.3.group.fill") }
                .tag(0)

            NavigationStack { AccountsView() }
                .tabItem { Label("账号", systemImage: "person.crop.rectangle.stack.fill") }
                .tag(1)

            NavigationStack { GroupsView() }
                .tabItem { Label("分组", systemImage: "square.stack.3d.up.fill") }
                .tag(2)

            NavigationStack { UsersView() }
                .tabItem { Label("用户", systemImage: "person.2.fill") }
                .tag(3)

            NavigationStack { OperationsView() }
                .tabItem { Label("设置", systemImage: "gearshape.fill") }
                .tag(4)
        }
        .tint(AppTheme.primary)
        .background(AppBackdrop())
        .toolbarBackground(.ultraThinMaterial, for: .tabBar)
    }
}

struct ErrorMessage: Identifiable {
    let id = UUID()
    let title: String
    let detail: String

    init(_ error: Error, title: String = "请求失败") {
        self.title = title
        self.detail = error.localizedDescription
    }
}

struct FeedbackMessage: Identifiable {
    enum Tone { case success, failure, notice }

    let id = UUID()
    let tone: Tone
    let title: String
    let detail: String
}

@MainActor
final class FeedbackCenter: ObservableObject {
    @Published var message: FeedbackMessage?

    func success(_ title: String = "操作成功", detail: String = "已完成并同步到 XIASS API。") {
        message = FeedbackMessage(tone: .success, title: title, detail: detail)
    }

    func failure(_ error: Error, title: String = "操作失败") {
        message = FeedbackMessage(tone: .failure, title: title, detail: error.localizedDescription)
    }

    func notice(_ title: String, detail: String) {
        message = FeedbackMessage(tone: .notice, title: title, detail: detail)
    }
}

extension View {
    func requestError(_ error: Binding<ErrorMessage?>) -> some View {
        alert(item: error) { message in
            Alert(title: Text(message.title), message: Text(message.detail), dismissButton: .default(Text("知道了")))
        }
    }
}
