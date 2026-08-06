import SwiftUI

struct LoginView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @State private var address = SecureStore.baseURL ?? "https://api.xiass.com"
    @State private var email = ""
    @State private var password = ""
    @State private var twoFactorCode = ""
    @State private var isSubmitting = false
    @State private var error: ErrorMessage?

    var body: some View {
        ScrollView {
            VStack(spacing: 24) {
                Spacer(minLength: 64)
                brand
                credentialPanel
                securityNote
                Spacer(minLength: 32)
            }
            .padding(.horizontal, 20)
            .frame(maxWidth: .infinity, minHeight: UIScreen.main.bounds.height)
        }
        .scrollDismissesKeyboard(.interactively)
        .dismissKeyboardOnTap()
        .requestError($error)
    }

    private var brand: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .center, spacing: 14) {
                GlassIcon(name: "shield.checkered", tint: AppTheme.primary)
                    .scaleEffect(1.25)
                VStack(alignment: .leading, spacing: 3) {
                    Text("XIASS 管理端")
                        .font(.system(.largeTitle, design: .rounded, weight: .bold))
                    Text("随时掌握 API、账号与用户状态")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 6)
    }

    private var credentialPanel: some View {
        GlassCard(tint: AppTheme.primary) {
            VStack(alignment: .leading, spacing: 18) {
                VStack(alignment: .leading, spacing: 6) {
                    Text("连接服务器")
                        .font(.headline)
                    TextField("https://api.xiass.com", text: $address)
                        .textInputAutocapitalization(.never)
                        .keyboardType(.URL)
                        .autocorrectionDisabled()
                        .textFieldStyle(.roundedBorder)
                }

                if session.pendingTwoFactorToken != nil {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("双重验证").font(.headline)
                        TextField("验证器动态码", text: $twoFactorCode)
                            .keyboardType(.numberPad)
                            .textContentType(.oneTimeCode)
                            .textFieldStyle(.roundedBorder)
                        Button(isSubmitting ? "正在验证…" : "验证并登录") { submitTwoFactor() }
                            .buttonStyle(.borderedProminent)
                            .controlSize(.large)
                            .disabled(isSubmitting || twoFactorCode.trimmingCharacters(in: .whitespaces).isEmpty)
                        Button("取消", role: .cancel) { session.cancelTwoFactor() }
                            .font(.subheadline)
                    }
                } else {
                    VStack(alignment: .leading, spacing: 10) {
                        Text("管理员登录").font(.headline)
                        TextField("邮箱", text: $email)
                            .textInputAutocapitalization(.never)
                            .keyboardType(.emailAddress)
                            .textContentType(.username)
                            .autocorrectionDisabled()
                            .textFieldStyle(.roundedBorder)
                        SecureField("密码", text: $password)
                            .textContentType(.password)
                            .textFieldStyle(.roundedBorder)
                        Button(isSubmitting ? "正在登录…" : "登录 XIASS API") { submitLogin() }
                            .buttonStyle(.borderedProminent)
                            .controlSize(.large)
                            .frame(maxWidth: .infinity)
                            .disabled(isSubmitting || email.trimmingCharacters(in: .whitespaces).isEmpty || password.isEmpty)
                    }
                }
            }
        }
    }

    private var securityNote: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "lock.fill").foregroundStyle(AppTheme.accent)
            Text("会话仅保存在本机 Keychain；上游账号凭证只在保存时传给 XIASS API，不保存在手机内。")
                .font(.footnote)
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 8)
    }

    private func submitLogin() {
        isSubmitting = true
        Task {
            defer { isSubmitting = false }
            do {
                try await session.signIn(address: address, email: email, password: password)
                password = ""
                if session.pendingTwoFactorToken == nil {
                    feedback.success("登录成功", detail: "已连接 XIASS API 管理端。")
                } else {
                    feedback.notice("需要双重验证", detail: "请输入验证器中的动态码以完成登录。")
                }
            } catch {
                self.error = ErrorMessage(error, title: "登录失败")
            }
        }
    }

    private func submitTwoFactor() {
        isSubmitting = true
        Task {
            defer { isSubmitting = false }
            do {
                try await session.completeTwoFactor(code: twoFactorCode)
                twoFactorCode = ""
                feedback.success("验证成功", detail: "已连接 XIASS API 管理端。")
            } catch {
                self.error = ErrorMessage(error, title: "验证失败")
            }
        }
    }
}
