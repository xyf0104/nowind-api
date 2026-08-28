package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContentModerationKeywordMatcherRuleModes(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		keywords []string
		want     string
		hit      bool
	}{
		{
			name:     "context rule matches terms in natural order",
			text:     "你帮我逆向一下网站",
			keywords: []string{"逆向 && 网站"},
			want:     "逆向 && 网站",
			hit:      true,
		},
		{
			name:     "context rule matches terms in either order",
			text:     "这个网站需要一点逆向分析",
			keywords: []string{"逆向 && 网站"},
			want:     "逆向 && 网站",
			hit:      true,
		},
		{
			name:     "single contextual term does not trigger",
			text:     "逆向都不行嘛",
			keywords: []string{"逆向 && 网站"},
			hit:      false,
		},
		{
			name:     "bare keyword is exact normalized phrase",
			text:     "  逆向  ",
			keywords: []string{"逆向"},
			want:     "逆向",
			hit:      true,
		},
		{
			name:     "bare keyword does not match a longer sentence",
			text:     "逆向都不行嘛",
			keywords: []string{"逆向"},
			hit:      false,
		},
		{
			name:     "explicit contains retains legacy capability",
			text:     "Please ignore the BadWord here",
			keywords: []string{"contains:badword"},
			want:     "contains:badword",
			hit:      true,
		},
		{
			name:     "configured order wins",
			text:     "网站的逆向分析",
			keywords: []string{"contains:网站", "逆向 && 网站"},
			want:     "contains:网站",
			hit:      true,
		},
		{
			name:     "invalid context rule is ignored",
			text:     "anything",
			keywords: []string{"&& 网站"},
			hit:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, hit := newContentModerationKeywordMatcher(tt.keywords).Match(tt.text)
			require.Equal(t, tt.hit, hit)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestContentModerationKeywordMatcherHandlesManyRules(t *testing.T) {
	keywords := make([]string, 0, 10002)
	for index := 0; index < 10000; index++ {
		keywords = append(keywords, "term"+strings.Repeat("x", index%7)+" && absent"+strings.Repeat("y", index%11))
	}
	keywords = append(keywords, "contains:do-not-match", "alpha && beta")

	keyword, hit := newContentModerationKeywordMatcher(keywords).Match("beta appears before alpha")
	require.True(t, hit)
	require.Equal(t, "alpha && beta", keyword)
}

func TestContentModerationKeywordRuleIsContextual(t *testing.T) {
	require.True(t, contentModerationKeywordRuleIsContextual("逆向 && 网站"))
	require.False(t, contentModerationKeywordRuleIsContextual("逆向"))
	require.False(t, contentModerationKeywordRuleIsContextual("contains:逆向"))
	require.False(t, contentModerationKeywordRuleIsContextual("逆向 && "))
}
