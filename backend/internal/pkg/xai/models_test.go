package xai

import "testing"

func TestStripGrokProviderPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "xai", input: "xai/grok-4.5", want: "grok-4.5"},
		{name: "x ai case insensitive", input: " X-AI/Grok-4.5 ", want: "Grok-4.5"},
		{name: "grok", input: "grok/grok-build", want: "grok-build"},
		{name: "native", input: " grok-4.3 ", want: "grok-4.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := StripGrokProviderPrefix(tt.input); got != tt.want {
				t.Fatalf("StripGrokProviderPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveGrokTextResponsesModelID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		defaultText []string
		want        string
	}{
		{name: "empty uses built in default", want: DefaultTextModel},
		{name: "empty uses caller default", defaultText: []string{"grok-4.6"}, want: "grok-4.6"},
		{name: "prefixed default alias uses caller default", input: "xai/grok-latest", defaultText: []string{"grok-4.6"}, want: "grok-4.6"},
		{name: "build alias", input: "GROK/GROK-BUILD", want: "grok-build-0.1"},
		{name: "dated multi agent alias", input: "grok-4.20-multi-agent-latest", want: "grok-4.20-multi-agent-0309"},
		{name: "unknown prefixed model remains usable", input: "x-ai/custom-grok", want: "custom-grok"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ResolveGrokTextResponsesModelID(tt.input, tt.defaultText...); got != tt.want {
				t.Fatalf("ResolveGrokTextResponsesModelID(%q, %v) = %q, want %q", tt.input, tt.defaultText, got, tt.want)
			}
		})
	}
}
