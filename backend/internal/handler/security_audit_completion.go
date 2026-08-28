package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/gin-gonic/gin"
)

// securityAuditNeedsCompletedResponse is deliberately limited to a confirmed
// policy rejection. Availability and malformed-audit failures remain errors so
// clients can retry a genuinely transient condition.
func securityAuditNeedsCompletedResponse(decision *securityaudit.Decision) bool {
	if decision == nil {
		return false
	}
	// This compatibility response is for the legacy content-moderation block
	// shown to OpenAI clients. Prompt-guard failures have their own protocol and
	// status contract and must remain errors.
	return decision.Legacy != nil && decision.Legacy.Blocked
}

func requestIsStreaming(c *gin.Context) bool {
	if c == nil {
		return false
	}
	stream, ok := c.Get(opsStreamKey)
	requested, _ := stream.(bool)
	return ok && requested
}

func inboundIsResponsesCompletionRoute(c *gin.Context) bool {
	return inboundIsResponses(c)
}

func inboundIsChatCompletionsRoute(c *gin.Context) bool {
	return strings.HasSuffix(inboundRequestPath(c), "/chat/completions")
}

func inboundRequestPath(c *gin.Context) string {
	if c == nil {
		return ""
	}
	p := strings.TrimRight(c.FullPath(), "/")
	if p == "" && c.Request != nil && c.Request.URL != nil {
		p = strings.TrimRight(c.Request.URL.Path, "/")
	}
	return p
}

func writeResponsesSecurityAuditCompletion(c *gin.Context, message string) {
	if c == nil {
		return
	}
	responseID := synthesizeResponseID(c)
	itemID := "msg_" + strings.TrimPrefix(responseID, "resp_")
	model := requestModel(c)
	completedItem := apicompat.ResponsesOutput{
		Type:   "message",
		ID:     itemID,
		Role:   "assistant",
		Status: "completed",
		Content: []apicompat.ResponsesContentPart{{
			Type: "output_text",
			Text: message,
		}},
	}
	completed := &apicompat.ResponsesResponse{
		ID:     responseID,
		Object: "response",
		Model:  model,
		Status: "completed",
		Output: []apicompat.ResponsesOutput{completedItem},
	}
	if !requestIsStreaming(c) {
		c.JSON(http.StatusOK, completed)
		return
	}

	created := &apicompat.ResponsesResponse{
		ID:     responseID,
		Object: "response",
		Model:  model,
		Status: "in_progress",
		Output: []apicompat.ResponsesOutput{},
	}
	inProgressItem := completedItem
	inProgressItem.Status = "in_progress"
	inProgressItem.Content = []apicompat.ResponsesContentPart{}
	part := apicompat.ResponsesContentPart{Type: "output_text", Text: message}
	events := []apicompat.ResponsesStreamEvent{
		{Type: "response.created", Response: created},
		{Type: "response.output_item.added", Item: &inProgressItem},
		{Type: "response.content_part.added", Part: &part, ItemID: itemID},
		{Type: "response.output_text.delta", Delta: message, ItemID: itemID},
		{Type: "response.output_text.done", Text: message, ItemID: itemID},
		{Type: "response.content_part.done", Part: &part, ItemID: itemID},
		{Type: "response.output_item.done", Item: &completedItem},
		{Type: "response.completed", Response: completed},
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			_ = c.Error(err)
			return
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event.Type, payload); err != nil {
			_ = c.Error(err)
			return
		}
	}
	if flusher != nil {
		flusher.Flush()
	}
}

func writeChatCompletionsSecurityAuditCompletion(c *gin.Context, message string) {
	if c == nil {
		return
	}
	responseID := "chatcmpl_" + strings.TrimPrefix(synthesizeResponseID(c), "resp_")
	model := requestModel(c)
	createdAt := time.Now().Unix()
	if !requestIsStreaming(c) {
		c.JSON(http.StatusOK, apicompat.ChatCompletionsResponse{
			ID:      responseID,
			Object:  "chat.completion",
			Created: createdAt,
			Model:   model,
			Choices: []apicompat.ChatChoice{{
				Index: 0,
				Message: apicompat.ChatMessage{
					Role:    "assistant",
					Content: json.RawMessage(fmt.Sprintf("%q", message)),
				},
				FinishReason: "stop",
			}},
		})
		return
	}

	finishReason := "stop"
	content := message
	chunks := []apicompat.ChatCompletionsChunk{
		{
			ID: responseID, Object: "chat.completion.chunk", Created: createdAt, Model: model,
			Choices: []apicompat.ChatChunkChoice{{Index: 0, Delta: apicompat.ChatDelta{Role: "assistant"}}},
		},
		{
			ID: responseID, Object: "chat.completion.chunk", Created: createdAt, Model: model,
			Choices: []apicompat.ChatChunkChoice{{Index: 0, Delta: apicompat.ChatDelta{Content: &content}}},
		},
		{
			ID: responseID, Object: "chat.completion.chunk", Created: createdAt, Model: model,
			Choices: []apicompat.ChatChunkChoice{{Index: 0, Delta: apicompat.ChatDelta{}, FinishReason: &finishReason}},
		},
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	for _, chunk := range chunks {
		payload, err := json.Marshal(chunk)
		if err != nil {
			_ = c.Error(err)
			return
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", payload); err != nil {
			_ = c.Error(err)
			return
		}
	}
	if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err != nil {
		_ = c.Error(err)
		return
	}
	if flusher != nil {
		flusher.Flush()
	}
}
