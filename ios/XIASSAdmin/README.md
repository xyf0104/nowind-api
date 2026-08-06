# XIASS 管理端 iOS

XIASS API 的原生 iPhone/iPad 管理端。它直接调用现有的 `/api/v1` 管理接口，不新增服务端、不改变 XIASS API 的网关或调度逻辑。

## 手机端包含的完整管理闭环

- 管理员登录、TOTP 二次验证、自动刷新登录态，以及 Keychain 安全保存会话。
- 总览、实时请求指标、账号健康状态和当日用量。
- 上游账号列表、搜索、按平台筛选、快速添加凭证、连接测试、调度开关、清除错误、恢复运行状态和优先级调整。账号详情和列表长按菜单中的“测试账号”都会实际调用现有 SSE 测试接口，支持文字与图片测试结果。
- 分组创建与启停。
- 用户创建、启停和余额调整。
- 最近用量、版本检查与服务更新。
- App 内置 GitHub 安装包检查：只识别发布资产名包含 `XIASSAdmin` 的 iOS Release，避免将 XIASS API 服务端版本误认为手机端更新。

所有网页后台功能都会保留：在“更多管理”顶部打开“完整后台”，即可在 App 内使用和网页端相同的 FRP、代理、模型价格、审计、批量操作、备份和系统设置等模块。原生页面负责移动端高频操作，完整后台负责不适合删减的全部模块；两者都直接调用现有 XIASS API，不新增服务端，也不改变网关或调度逻辑。

完整后台使用独立的网页登录会话。这避免网页与原生 App 争用一次性轮换的刷新令牌，导致任一端突然退出登录；第一次打开时按网页端正常登录一次即可，之后会在 WebView 内保持会话。

## 在 Xcode 中安装到真机

1. 打开 `XIASSAdmin.xcodeproj`。
2. 选中 `XIASSAdmin` target，在 **Signing & Capabilities** 中选择你现有的 Apple Developer Team。
3. 如已有相同 Bundle ID，把 `com.xiass.admin` 改成你 Team 下未占用的唯一 ID。
4. 连接 iPhone，选择设备后直接运行。

工程没有写死任何 Team ID，也不会改动你的 Xcode 全局签名设置。

## 发布 GitHub 安装包与手机端更新

App 的“更新中心”会扫描 GitHub 最近 30 个 Release，并只识别同时带有 `XIASSAdmin` 签名 IPA 和 OTA manifest 的发布。源码 ZIP 不会被误认为可安装更新。发布完成后，手机端点“检查全部更新”即可看到新版本。

推荐发布 `ios-v1.0.1` 这类独立于服务端的 iOS 标签，避免服务端与 App 的版本号互相干扰。发布资产需要使用以下固定命名：

- `XIASSAdmin-1.0.1.ipa`：使用 Apple 分发证书签名的安装包。
- `XIASSAdmin-1.0.1.plist`：对应的 OTA manifest。App 检测到它后会交给 iOS 系统发起安装。

首次配置好 Apple Developer Team、Ad Hoc provisioning profile 和已注册设备后，可以在仓库根目录运行：

```bash
./ios/XIASSAdmin/scripts/release-to-github.sh 1.0.4 YOUR_TEAM_ID com.yourcompany.xiassadmin
```

脚本会归档、导出 IPA、生成 OTA manifest，并上传到 `xyf0104/xiass-api` 的 GitHub Release。它不保存 Team ID、证书、设备 UDID 或 API 密钥。

注意：iOS 不允许普通 App 自己覆盖安装。要在手机上直接安装，必须使用有效的 TestFlight、Ad Hoc 或企业分发签名；个人免费 Team 仍可在 Xcode 里连接真机直接运行。GitHub 资产始终保留，未使用 Ad Hoc 签名时 App 会提供 IPA 或源码下载入口，供你在本机 Xcode 用自己的 Team 签名安装。

## 命令行构建

```bash
cd ios/XIASSAdmin
xcodegen generate
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer \
  xcodebuild -project XIASSAdmin.xcodeproj -scheme XIASSAdmin \
  -sdk iphonesimulator -destination 'platform=iOS Simulator,name=iPhone 17 Pro' build
```

Open `XIASSAdmin.xcodeproj` in Xcode, select the existing XIASS Apple Developer Team, and choose a unique bundle identifier before installing on a physical device.
