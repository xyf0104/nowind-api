package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// parseTeamMailboxConfigImport accepts both the legacy registration tool's
// JSON shape and the normalized TEAM_CHILD_MAIL_*.env fragment.
func parseTeamMailboxConfigImport(body []byte, filename string) (teamMailboxConfigValues, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return teamMailboxConfigValues{}, fmt.Errorf("配置文件为空")
	}
	if trimmed[0] == '{' {
		var raw map[string]any
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return teamMailboxConfigValues{}, fmt.Errorf("JSON 配置文件格式无效")
		}
		values := teamMailboxConfigValues{
			baseURL:      configImportString(raw, "cloudflare_api_base", "TEAM_CHILD_MAIL_API_BASE"),
			authMode:     configImportString(raw, "cloudflare_auth_mode", "TEAM_CHILD_MAIL_AUTH_MODE"),
			apiKey:       configImportString(raw, "cloudflare_api_key", "TEAM_CHILD_MAIL_API_KEY"),
			customAuth:   configImportString(raw, "cloudflare_custom_auth", "TEAM_CHILD_MAIL_CUSTOM_AUTH"),
			domain:       configImportString(raw, "defaultDomains", "TEAM_CHILD_MAIL_DOMAIN"),
			createPath:   configImportString(raw, "cloudflare_path_accounts", "TEAM_CHILD_MAIL_CREATE_PATH"),
			messagesPath: configImportString(raw, "cloudflare_path_messages", "TEAM_CHILD_MAIL_MESSAGES_PATH"),
		}
		values.domain = strings.TrimSpace(strings.Split(values.domain, ",")[0])
		return values, nil
	}

	values := teamMailboxConfigValues{}
	for _, line := range strings.Split(string(trimmed), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			} else {
				value = value[1 : len(value)-1]
			}
		}
		switch key {
		case "TEAM_CHILD_MAIL_API_BASE":
			values.baseURL = value
		case "TEAM_CHILD_MAIL_AUTH_MODE":
			values.authMode = value
		case "TEAM_CHILD_MAIL_API_KEY":
			values.apiKey = value
		case "TEAM_CHILD_MAIL_CUSTOM_AUTH":
			values.customAuth = value
		case "TEAM_CHILD_MAIL_DOMAIN":
			values.domain = value
		case "TEAM_CHILD_MAIL_CREATE_PATH":
			values.createPath = value
		case "TEAM_CHILD_MAIL_MESSAGES_PATH":
			values.messagesPath = value
		}
	}
	if strings.TrimSpace(values.baseURL) == "" {
		name := filename
		if name == "" {
			name = "配置文件"
		}
		return teamMailboxConfigValues{}, fmt.Errorf("无法从 %s 读取邮箱服务地址", name)
	}
	return values, nil
}

func configImportString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
