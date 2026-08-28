package service

import "strings"

type contentModerationKeywordMatchMode uint8

const (
	contentModerationKeywordMatchExact contentModerationKeywordMatchMode = iota
	contentModerationKeywordMatchContains
	contentModerationKeywordMatchAll
)

type contentModerationKeywordRuleSpec struct {
	mode      contentModerationKeywordMatchMode
	terms     []string
	exactText string
	valid     bool
}

// contentModerationKeywordMatcher evaluates administrator rules without
// treating every configured word as an arbitrary substring. A bare line is an
// exact normalized phrase. Use `term A && term B` for a contextual rule, or
// `contains:term` when substring matching is explicitly intended.
type contentModerationKeywordMatcher struct {
	nodes           []contentModerationKeywordNode
	edges           []contentModerationKeywordEdge
	outputs         []int32
	rootTransitions [256]int32
	terms           []string
	rules           []contentModerationKeywordRule
}

type contentModerationKeywordRule struct {
	raw     string
	mode    contentModerationKeywordMatchMode
	termIDs []int32
	exact   string
}

type contentModerationKeywordNode struct {
	failure     int32
	edgeStart   uint32
	edgeCount   uint32
	outputStart uint32
	outputCount uint32
}

type contentModerationKeywordEdge struct {
	target int32
	label  byte
}

type contentModerationKeywordBuildNode struct {
	children  map[byte]int32
	terminals []int32
	outputs   []int32
	failure   int32
}

func newContentModerationKeywordMatcher(keywords []string) *contentModerationKeywordMatcher {
	if len(keywords) == 0 {
		return nil
	}

	termIndexes := make(map[string]int32)
	terms := make([]string, 0)
	rules := make([]contentModerationKeywordRule, 0, len(keywords))
	for _, raw := range keywords {
		keyword := strings.TrimSpace(raw)
		if keyword == "" {
			continue
		}
		spec := parseContentModerationKeywordRule(keyword)
		if !spec.valid {
			continue
		}
		rule := contentModerationKeywordRule{raw: keyword, mode: spec.mode, exact: spec.exactText}
		if spec.mode == contentModerationKeywordMatchExact {
			rules = append(rules, rule)
			continue
		}
		rule.termIDs = make([]int32, 0, len(spec.terms))
		for _, term := range spec.terms {
			termID, exists := termIndexes[term]
			if !exists {
				termID = int32(len(terms))
				termIndexes[term] = termID
				terms = append(terms, term)
			}
			rule.termIDs = append(rule.termIDs, termID)
		}
		rules = append(rules, rule)
	}
	if len(rules) == 0 {
		return nil
	}

	matcher := &contentModerationKeywordMatcher{terms: terms, rules: rules}
	if len(terms) == 0 {
		return matcher
	}
	matcher.buildAutomaton()
	return matcher
}

func parseContentModerationKeywordRule(raw string) contentModerationKeywordRuleSpec {
	keyword := strings.TrimSpace(raw)
	if keyword == "" {
		return contentModerationKeywordRuleSpec{}
	}
	if strings.HasPrefix(strings.ToLower(keyword), "contains:") {
		term := strings.TrimSpace(keyword[len("contains:"):])
		if term == "" {
			return contentModerationKeywordRuleSpec{}
		}
		return contentModerationKeywordRuleSpec{
			mode:  contentModerationKeywordMatchContains,
			terms: []string{strings.ToLower(term)},
			valid: true,
		}
	}
	if strings.Contains(keyword, "&&") {
		parts := strings.Split(keyword, "&&")
		seen := make(map[string]struct{}, len(parts))
		terms := make([]string, 0, len(parts))
		for _, rawPart := range parts {
			term := strings.ToLower(strings.TrimSpace(rawPart))
			if term == "" {
				return contentModerationKeywordRuleSpec{}
			}
			if _, exists := seen[term]; exists {
				continue
			}
			seen[term] = struct{}{}
			terms = append(terms, term)
		}
		if len(terms) < 2 {
			return contentModerationKeywordRuleSpec{}
		}
		return contentModerationKeywordRuleSpec{mode: contentModerationKeywordMatchAll, terms: terms, valid: true}
	}
	return contentModerationKeywordRuleSpec{
		mode:      contentModerationKeywordMatchExact,
		exactText: normalizeContentModerationKeywordText(keyword),
		valid:     true,
	}
}

func normalizeContentModerationKeywordText(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
}

func (m *contentModerationKeywordMatcher) buildAutomaton() {
	buildNodes := []contentModerationKeywordBuildNode{{children: make(map[byte]int32)}}
	for termID, term := range m.terms {
		state := int32(0)
		for _, label := range []byte(term) {
			next, exists := buildNodes[state].children[label]
			if !exists {
				next = int32(len(buildNodes))
				buildNodes[state].children[label] = next
				buildNodes = append(buildNodes, contentModerationKeywordBuildNode{children: make(map[byte]int32)})
			}
			state = next
		}
		buildNodes[state].terminals = append(buildNodes[state].terminals, int32(termID))
	}
	for index := range buildNodes {
		buildNodes[index].outputs = append(buildNodes[index].outputs, buildNodes[index].terminals...)
	}

	queue := make([]int32, 0, len(buildNodes)-1)
	for _, target := range buildNodes[0].children {
		queue = append(queue, target)
	}
	for label, target := range buildNodes[0].children {
		m.rootTransitions[label] = target
	}
	for queueIndex := 0; queueIndex < len(queue); queueIndex++ {
		state := queue[queueIndex]
		for label, target := range buildNodes[state].children {
			fallback := buildNodes[state].failure
			for {
				next, exists := buildNodes[fallback].children[label]
				if exists && next != target {
					fallback = next
					break
				}
				if fallback == 0 {
					fallback = 0
					break
				}
				fallback = buildNodes[fallback].failure
			}
			buildNodes[target].failure = fallback
			buildNodes[target].outputs = append(buildNodes[target].outputs, buildNodes[fallback].outputs...)
			queue = append(queue, target)
		}
	}

	m.nodes = make([]contentModerationKeywordNode, len(buildNodes))
	m.edges = make([]contentModerationKeywordEdge, 0, len(m.terms)*2)
	m.outputs = make([]int32, 0, len(m.terms)*2)
	for index, buildNode := range buildNodes {
		labels := make([]byte, 0, len(buildNode.children))
		for label := range buildNode.children {
			labels = append(labels, label)
		}
		// Deterministic edge ordering makes the compiled matcher reproducible.
		for left := 1; left < len(labels); left++ {
			label := labels[left]
			insertAt := left
			for insertAt > 0 && label < labels[insertAt-1] {
				labels[insertAt] = labels[insertAt-1]
				insertAt--
			}
			labels[insertAt] = label
		}
		m.nodes[index] = contentModerationKeywordNode{
			failure:     buildNode.failure,
			edgeStart:   uint32(len(m.edges)),
			edgeCount:   uint32(len(labels)),
			outputStart: uint32(len(m.outputs)),
			outputCount: uint32(len(buildNode.outputs)),
		}
		for _, label := range labels {
			m.edges = append(m.edges, contentModerationKeywordEdge{target: buildNode.children[label], label: label})
		}
		m.outputs = append(m.outputs, buildNode.outputs...)
	}
}

func (m *contentModerationKeywordMatcher) Match(text string) (string, bool) {
	if m == nil || text == "" || len(m.rules) == 0 {
		return "", false
	}
	normalizedText := normalizeContentModerationKeywordText(text)
	matchedTerms := make([]bool, len(m.terms))
	if len(m.nodes) > 0 {
		state := int32(0)
		for _, label := range []byte(strings.ToLower(text)) {
			for {
				next := m.next(state, label)
				if next != 0 {
					state = next
					break
				}
				if state == 0 {
					break
				}
				state = m.nodes[state].failure
			}
			node := m.nodes[state]
			for outputIndex := node.outputStart; outputIndex < node.outputStart+node.outputCount; outputIndex++ {
				termID := m.outputs[outputIndex]
				if termID >= 0 && int(termID) < len(matchedTerms) {
					matchedTerms[termID] = true
				}
			}
		}
	}

	for _, rule := range m.rules {
		matched := false
		switch rule.mode {
		case contentModerationKeywordMatchExact:
			matched = normalizedText == rule.exact
		case contentModerationKeywordMatchContains:
			matched = len(rule.termIDs) == 1 && matchedTerms[rule.termIDs[0]]
		case contentModerationKeywordMatchAll:
			matched = true
			for _, termID := range rule.termIDs {
				if termID < 0 || int(termID) >= len(matchedTerms) || !matchedTerms[termID] {
					matched = false
					break
				}
			}
		}
		if matched {
			return rule.raw, true
		}
	}
	return "", false
}

func (m *contentModerationKeywordMatcher) next(state int32, label byte) int32 {
	if m == nil || state < 0 || int(state) >= len(m.nodes) {
		return 0
	}
	if state == 0 {
		return m.rootTransitions[label]
	}
	node := m.nodes[state]
	left := int(node.edgeStart)
	right := left + int(node.edgeCount)
	for left < right {
		middle := left + (right-left)/2
		edge := m.edges[middle]
		if edge.label < label {
			left = middle + 1
			continue
		}
		right = middle
	}
	end := int(node.edgeStart) + int(node.edgeCount)
	if left < end && m.edges[left].label == label {
		return m.edges[left].target
	}
	return 0
}
