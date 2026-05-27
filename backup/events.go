package backup

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"

	"github.com/aws/aws-lambda-go/events"
)

// EventHandler adapts a Handler to AWS Lambda invocations: EventBridge
// schedules (and direct invokes) run a deduplicated backup, while API Gateway
// v2 HTTP requests to /run run an authenticated, forced backup.
type EventHandler struct {
	handler *Handler
	apiKey  string
}

// NewEventHandler wraps h, authenticating HTTP requests against apiKey.
func NewEventHandler(h *Handler, apiKey string) *EventHandler {
	return &EventHandler{handler: h, apiKey: apiKey}
}

// Dispatch routes a raw Lambda event to the HTTP handler when it is an API
// Gateway v2 request, or to a scheduled (deduplicated) backup otherwise.
func (e *EventHandler) Dispatch(ctx context.Context, raw json.RawMessage) (any, error) {
	var req events.APIGatewayV2HTTPRequest
	if err := json.Unmarshal(raw, &req); err == nil && req.RequestContext.HTTP.Method != "" {
		return e.handleHTTP(ctx, req), nil
	}

	// Scheduled or direct invocation: no HTTP response expected, dedupe applies.
	_, err := e.handler.Run(ctx, false)
	return nil, err
}

// handleHTTP authenticates the request, runs a forced backup, and returns an
// HTTP response. It never returns an error so failures surface as HTTP status
// codes rather than Lambda errors.
func (e *EventHandler) handleHTTP(ctx context.Context, req events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse {
	if !e.authorized(req) {
		return jsonResponse(401, map[string]string{"status": "error", "error": "unauthorized"})
	}

	result, err := e.handler.Run(ctx, true)
	if err != nil {
		log.Printf("backup failed: %v", err)
		return jsonResponse(500, map[string]string{"status": "error", "error": err.Error()})
	}
	return jsonResponse(200, result)
}

// authorized reports whether the request carries the configured API key, via
// the X-Api-Key header or the api_key query string parameter.
func (e *EventHandler) authorized(req events.APIGatewayV2HTTPRequest) bool {
	if e.apiKey == "" {
		log.Println("API key not configured; rejecting request")
		return false
	}
	// API Gateway v2 lower-cases header names.
	if provided, ok := req.Headers["x-api-key"]; ok && constantTimeEqual(provided, e.apiKey) {
		return true
	}
	if provided, ok := req.QueryStringParameters["api_key"]; ok && constantTimeEqual(provided, e.apiKey) {
		return true
	}
	return false
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func jsonResponse(status int, body any) events.APIGatewayV2HTTPResponse {
	b, _ := json.Marshal(body)
	return events.APIGatewayV2HTTPResponse{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(b),
	}
}
