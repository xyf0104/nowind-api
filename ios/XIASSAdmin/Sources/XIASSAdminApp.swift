import SwiftUI

@main
struct XIASSAdminApp: App {
    @StateObject private var session = AppSession()
    @StateObject private var feedback = FeedbackCenter()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(session)
                .environmentObject(feedback)
        }
    }
}
