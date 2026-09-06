package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

type openAISSEDataAccumulator struct {
	lines     []string
	eventType string
}

func (a *openAISSEDataAccumulator) AddLine(line string, fn func([]byte)) {
	if fn == nil {
		return
	}
	trimmedLine := strings.TrimRight(line, "\r\n")
	if eventType, ok := extractOpenAISSEEventLine(trimmedLine); ok {
		a.eventType = eventType
		return
	}
	if data, ok := extractOpenAISSEDataLine(trimmedLine); ok {
		a.lines = append(a.lines, data)
		return
	}
	if strings.TrimSpace(trimmedLine) == "" {
		a.Flush(fn)
	}
}

func (a *openAISSEDataAccumulator) Flush(fn func([]byte)) {
	if fn == nil {
		return
	}
	eventType := a.eventType
	a.eventType = ""
	emitOpenAISSEDataPayloads(a.lines, func(data []byte) {
		if eventType != "" && gjson.ValidBytes(data) {
			data = []byte(openAICompatPayloadWithEventType(string(data), eventType))
		}
		fn(data)
	})
	a.lines = a.lines[:0]
}

func forEachOpenAISSEDataPayload(body string, fn func([]byte)) {
	if fn == nil || strings.TrimSpace(body) == "" {
		return
	}
	var acc openAISSEDataAccumulator
	for _, line := range strings.Split(body, "\n") {
		acc.AddLine(line, fn)
	}
	acc.Flush(fn)
}

func emitOpenAISSEDataPayloads(lines []string, fn func([]byte)) {
	if fn == nil || len(lines) == 0 {
		return
	}
	if len(lines) == 1 {
		emitOpenAISSEDataPayload(lines[0], fn)
		return
	}
	joined := strings.Join(lines, "\n")
	if gjson.Valid(joined) {
		emitOpenAISSEDataPayload(joined, fn)
		return
	}
	for _, line := range lines {
		emitOpenAISSEDataPayload(line, fn)
	}
}

func emitOpenAISSEDataPayload(data string, fn func([]byte)) {
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return
	}
	fn([]byte(data))
}
