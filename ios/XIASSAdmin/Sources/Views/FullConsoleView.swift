import SwiftUI
import WebKit

struct FullConsoleView: View {
    @Environment(\.dismiss) private var dismiss
    let url: URL
    let title: String

    init(url: URL, title: String = "XIASS 完整后台") {
        self.url = url
        self.title = title
    }

    var body: some View {
        ZStack(alignment: .topLeading) {
            WebConsole(url: url)
                .ignoresSafeArea()

            GeometryReader { proxy in
                Button { dismiss() } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 15, weight: .bold))
                        .foregroundStyle(.primary)
                        .frame(width: 42, height: 42)
                        .background(.ultraThinMaterial, in: Circle())
                        .overlay { Circle().stroke(.white.opacity(0.25), lineWidth: 0.8) }
                }
                .accessibilityLabel("关闭\(title)")
                .padding(.leading, 18)
                .padding(.top, proxy.safeAreaInsets.top + 8)
            }
        }
        .statusBarHidden(false)
    }
}

private struct WebConsole: UIViewRepresentable {
    let url: URL

    func makeUIView(context: Context) -> WKWebView {
        let preferences = WKWebpagePreferences()
        preferences.allowsContentJavaScript = true
        let configuration = WKWebViewConfiguration()
        configuration.defaultWebpagePreferences = preferences
        let webView = WKWebView(frame: .zero, configuration: configuration)
        webView.allowsBackForwardNavigationGestures = true
        webView.scrollView.contentInsetAdjustmentBehavior = .never
        webView.load(URLRequest(url: url))
        return webView
    }

    func updateUIView(_ webView: WKWebView, context: Context) {}
}
