import Foundation

struct GitHubReleaseAsset: Decodable, Hashable, Identifiable {
    let id: Int
    let name: String
    let browserDownloadURL: URL

    enum CodingKeys: String, CodingKey {
        case id, name
        case browserDownloadURL = "browser_download_url"
    }
}

struct GitHubRelease: Decodable, Hashable {
    let tagName: String
    let name: String?
    let htmlURL: URL
    let publishedAt: String?
    let prerelease: Bool
    let draft: Bool
    let assets: [GitHubReleaseAsset]

    enum CodingKeys: String, CodingKey {
        case name, assets, prerelease, draft
        case tagName = "tag_name"
        case htmlURL = "html_url"
        case publishedAt = "published_at"
    }

    var otaManifestAsset: GitHubReleaseAsset? {
        assets.first { asset in
            isXIASSAdminAsset(asset, withExtension: ".plist")
        }
    }

    var installerAsset: GitHubReleaseAsset? {
        assets.first { asset in
            isXIASSAdminAsset(asset, withExtension: ".ipa")
        }
    }

    var hasInstallableIOSDistribution: Bool {
        // An OTA installation requires both the signed IPA and its manifest.
        // Source ZIPs are useful to developers, but they cannot update an app.
        otaManifestAsset != nil && installerAsset != nil
    }

    var otaInstallURL: URL? {
        guard let manifestURL = otaManifestAsset?.browserDownloadURL else { return nil }
        var components = URLComponents()
        components.scheme = "itms-services"
        components.queryItems = [
            URLQueryItem(name: "action", value: "download-manifest"),
            URLQueryItem(name: "url", value: manifestURL.absoluteString)
        ]
        return components.url
    }

    private func isXIASSAdminAsset(_ asset: GitHubReleaseAsset, withExtension fileExtension: String) -> Bool {
        let name = asset.name.lowercased()
        return (name.hasPrefix("xiassadmin-") || name.hasPrefix("xiass-admin-")) && name.hasSuffix(fileExtension)
    }
}

enum GitHubReleaseService {
    private static let releasesURL = URL(string: "https://api.github.com/repos/xyf0104/xiass-api/releases?per_page=30")!

    static func latestIOSRelease() async throws -> GitHubRelease? {
        var request = URLRequest(url: releasesURL)
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        request.setValue("XIASSAdmin", forHTTPHeaderField: "User-Agent")
        request.timeoutInterval = 20
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            throw APIError(message: "无法读取 GitHub 最新发布信息。")
        }
        let releases = try JSONDecoder().decode([GitHubRelease].self, from: data)
        return releases.first { !$0.draft && $0.hasInstallableIOSDistribution }
    }

    static var currentVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "1.0.0"
    }

    static func isNewer(_ tag: String, than current: String = currentVersion) -> Bool {
        let latest = numericComponents(tag)
        let installed = numericComponents(current)
        guard !latest.isEmpty else { return false }
        let maxCount = max(latest.count, installed.count)
        for index in 0..<maxCount {
            let left = index < latest.count ? latest[index] : 0
            let right = index < installed.count ? installed[index] : 0
            if left != right { return left > right }
        }
        return false
    }

    private static func numericComponents(_ value: String) -> [Int] {
        value.split(whereSeparator: { !$0.isNumber }).compactMap { Int($0) }
    }
}
