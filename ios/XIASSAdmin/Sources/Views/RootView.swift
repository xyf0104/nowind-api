import SwiftUI

struct RootView: View {
    @EnvironmentObject private var session: AppSession

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
    }
}

struct AdminTabView: View {
    var body: some View {
        TabView {
            NavigationStack { DashboardView() }
                .tabItem { Label("概览", systemImage: "rectangle.3.group.fill") }

            NavigationStack { AccountsView() }
                .tabItem { Label("账号", systemImage: "person.crop.rectangle.stack.fill") }

            NavigationStack { GroupsView() }
                .tabItem { Label("分组", systemImage: "square.stack.3d.up.fill") }

            NavigationStack { UsersView() }
                .tabItem { Label("用户", systemImage: "person.2.fill") }

            NavigationStack { OperationsView() }
                .tabItem { Label("更多", systemImage: "ellipsis.circle.fill") }
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

extension View {
    func requestError(_ error: Binding<ErrorMessage?>) -> some View {
        alert(item: error) { message in
            Alert(title: Text(message.title), message: Text(message.detail), dismissButton: .default(Text("知道了")))
        }
    }
}
