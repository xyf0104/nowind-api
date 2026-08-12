//go:build embed || unit

package web

import (
	"net/http"
	"regexp"
	"strings"
)

// Vite emits content-hashed filenames under assets/, so the backend can apply
// immutable caching without relying on a reverse proxy to classify paths.
const staticAssetsCacheControl = "public, max-age=31536000, immutable"
const stableBrandCacheControl = "no-cache"

// Theme videos keep their stable names so the initial HTML can reference them
// before the application bundle starts. Cache them for a short browser window:
// repeat visits avoid re-downloading the moving background, while releases can
// still refresh the asset without waiting for a year-long immutable cache.
const themeMediaCacheControl = "public, max-age=86400, stale-while-revalidate=604800"

var viteHashedAssetPattern = regexp.MustCompile(`^assets/(?:.+/)?[^/]+-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9.]+$`)

// isFingerprintedEmbeddedAssetPath reports whether a cleaned URL path refers to
// a Vite asset whose filename contains the default eight-character build hash.
func isFingerprintedEmbeddedAssetPath(cleanPath string) bool {
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	return viteHashedAssetPattern.MatchString(cleanPath)
}

func isStableBrandStaticPath(cleanPath string) bool {
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	switch cleanPath {
	case "logo.png", "favicon.png", "favicon.ico", "favicon-dark.png", "favicon-light.png", "apple-touch-icon.png", "site.webmanifest":
		return true
	default:
		return strings.HasPrefix(cleanPath, "brand/")
	}
}

func isThemeMediaStaticPath(cleanPath string) bool {
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	switch cleanPath {
	case "media/xiass-dark-bokeh.mp4", "media/xiass-light-water.mp4", "media/xiass-dark-bokeh-poster.png", "media/xiass-light-water-poster.png":
		return true
	default:
		return false
	}
}

// applyStaticAssetCacheHeaders sets Cache-Control for long-cacheable static paths.
// index.html / SPA routes must keep no-cache and are not handled here.
func applyStaticAssetCacheHeaders(header http.Header, cleanPath string) {
	if header == nil {
		return
	}
	if isFingerprintedEmbeddedAssetPath(cleanPath) {
		header.Set("Cache-Control", staticAssetsCacheControl)
		return
	}
	if isThemeMediaStaticPath(cleanPath) {
		header.Set("Cache-Control", themeMediaCacheControl)
		return
	}
	if isStableBrandStaticPath(cleanPath) {
		header.Set("Cache-Control", stableBrandCacheControl)
	}
}
