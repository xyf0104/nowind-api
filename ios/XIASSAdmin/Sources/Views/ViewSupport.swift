import SwiftUI
import UIKit

enum AppTheme {
    static let primary = Color(red: 0.18, green: 0.67, blue: 0.98)
    static let accent = Color(red: 0.18, green: 0.86, blue: 0.67)
    static let canvasTop = Color(red: 0.035, green: 0.09, blue: 0.17)
    static let canvasBottom = Color(red: 0.07, green: 0.20, blue: 0.25)
    static let panelRadius: CGFloat = 24
}

struct AppBackdrop: View {
    var body: some View {
        LinearGradient(
            colors: [AppTheme.canvasTop, Color(red: 0.04, green: 0.16, blue: 0.24), AppTheme.canvasBottom],
            startPoint: .topLeading,
            endPoint: .bottomTrailing
        )
        .overlay(alignment: .top) {
            LinearGradient(
                colors: [Color.cyan.opacity(0.16), .clear],
                startPoint: .top,
                endPoint: .bottom
            )
            .frame(height: 260)
        }
        .ignoresSafeArea()
    }
}

struct AppScreen<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        ZStack {
            AppBackdrop()
            content
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .tint(AppTheme.primary)
        .preferredColorScheme(.dark)
    }
}

struct GlassCard<Content: View>: View {
    let tint: Color
    let content: Content

    init(tint: Color = .white, @ViewBuilder content: () -> Content) {
        self.tint = tint
        self.content = content()
    }

    var body: some View {
        content
            .padding(16)
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: AppTheme.panelRadius, style: .continuous))
            .background(tint.opacity(0.13), in: RoundedRectangle(cornerRadius: AppTheme.panelRadius, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: AppTheme.panelRadius, style: .continuous)
                    .stroke(.white.opacity(0.22), lineWidth: 0.8)
            }
            .shadow(color: .black.opacity(0.16), radius: 16, y: 8)
    }
}

struct GlassIcon: View {
    let name: String
    let tint: Color

    var body: some View {
        Image(systemName: name)
            .font(.system(size: 16, weight: .semibold))
            .foregroundStyle(tint)
            .frame(width: 38, height: 38)
            .background(tint.opacity(0.16), in: RoundedRectangle(cornerRadius: 13, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 13, style: .continuous)
                    .stroke(.white.opacity(0.16), lineWidth: 0.8)
            }
    }
}

struct GlassActionRow: View {
    let title: String
    let detail: String?
    let icon: String
    let tint: Color

    init(_ title: String, detail: String? = nil, icon: String, tint: Color = AppTheme.primary) {
        self.title = title
        self.detail = detail
        self.icon = icon
        self.tint = tint
    }

    var body: some View {
        HStack(spacing: 12) {
            GlassIcon(name: icon, tint: tint)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.body.weight(.semibold))
                if let detail, !detail.isEmpty {
                    Text(detail).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            Spacer(minLength: 8)
            Image(systemName: "chevron.right")
                .font(.caption.weight(.bold))
                .foregroundStyle(.tertiary)
        }
        .contentShape(Rectangle())
    }
}

extension View {
    func appScreenStyle() -> some View {
        background(AppBackdrop())
            .scrollContentBackground(.hidden)
            .toolbarBackground(.clear, for: .navigationBar)
    }

    func dismissKeyboardOnTap() -> some View {
        simultaneousGesture(
            TapGesture().onEnded {
                UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
            }
        )
    }
}

enum DisplayFormat {
    static func integer(_ value: Int?) -> String {
        guard let value else { return "--" }
        return value.formatted(.number.grouping(.automatic))
    }

    static func decimal(_ value: Double?, digits: Int = 2) -> String {
        guard let value else { return "--" }
        return value.formatted(.number.precision(.fractionLength(digits)))
    }

    static func currency(_ value: Double?) -> String {
        guard let value else { return "--" }
        return value.formatted(.currency(code: "USD").precision(.fractionLength(2)))
    }

    static func shortDate(_ value: String?) -> String {
        guard let value, !value.isEmpty else { return "--" }
        return String(value.prefix(16)).replacingOccurrences(of: "T", with: " ")
    }
}

struct StatusPill: View {
    let text: String

    private var color: Color {
        switch text.lowercased() {
        case "active", "normal", "healthy", "success", "paid", "completed": return .green
        case "error", "failed", "disabled", "cancelled", "expired": return .red
        case "paused", "inactive", "rate limited", "overloaded", "pending", "recharging", "refunding": return .orange
        default: return .secondary
        }
    }

    private var displayText: String {
        switch text.lowercased() {
        case "active", "normal", "healthy", "success": return "正常"
        case "error", "failed": return "异常"
        case "disabled": return "停用"
        case "paused", "inactive": return "暂停"
        case "rate limited": return "限流"
        case "overloaded": return "过载"
        case "pending": return "待支付"
        case "paid": return "已支付"
        case "recharging": return "充值中"
        case "completed": return "已完成"
        case "cancelled": return "已取消"
        case "expired": return "已过期"
        case "refunding": return "退款中"
        default: return text
        }
    }

    var body: some View {
        Text(displayText)
            .font(.caption.weight(.semibold))
            .foregroundStyle(color)
            .lineLimit(1)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(color.opacity(0.14), in: Capsule())
    }
}

struct MetricTile: View {
    let title: String
    let value: String
    let systemImage: String
    let tint: Color

    var body: some View {
        GlassCard(tint: tint) {
            VStack(alignment: .leading, spacing: 9) {
                GlassIcon(name: systemImage, tint: tint)
                Text(value)
                    .font(.system(.title3, design: .rounded, weight: .bold))
                    .monospacedDigit()
                    .lineLimit(1)
                    .minimumScaleFactor(0.7)
                Text(title)
                    .font(.caption.weight(.medium))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity, minHeight: 116, alignment: .leading)
        }
    }
}

struct EmptyState: View {
    let title: String
    let systemImage: String
    let detail: String

    var body: some View {
        GlassCard(tint: AppTheme.primary) {
            VStack(spacing: 12) {
                GlassIcon(name: systemImage, tint: AppTheme.primary)
                Text(title).font(.headline)
                Text(detail).font(.subheadline).foregroundStyle(.secondary).multilineTextAlignment(.center)
            }
            .padding(8)
            .frame(maxWidth: .infinity, minHeight: 220)
        }
        .padding()
    }
}
