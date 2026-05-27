package backup

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func eventHandler(f *fakeS3, apiKey string, dump Dumper) *EventHandler {
	h := New(Config{S3: f, Bucket: "b", Dump: dump})
	h.now = fixedClock(time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC))
	return NewEventHandler(h, apiKey)
}

func httpRequest(headers, query map[string]string) events.APIGatewayV2HTTPRequest {
	req := events.APIGatewayV2HTTPRequest{
		Headers:               headers,
		QueryStringParameters: query,
	}
	req.RequestContext.HTTP.Method = "GET"
	return req
}

func TestAuthorized(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		headers map[string]string
		query   map[string]string
		want    bool
	}{
		{"no key configured", "", map[string]string{"x-api-key": "secret"}, nil, false},
		{"header match", "secret", map[string]string{"x-api-key": "secret"}, nil, true},
		{"header wrong", "secret", map[string]string{"x-api-key": "nope"}, nil, false},
		{"query match", "secret", nil, map[string]string{"api_key": "secret"}, true},
		{"query wrong", "secret", nil, map[string]string{"api_key": "nope"}, false},
		{"neither", "secret", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := eventHandler(newFakeS3(), tt.apiKey, staticDump([]byte("x")))
			if got := e.authorized(httpRequest(tt.headers, tt.query)); got != tt.want {
				t.Errorf("authorized = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDispatchHTTPSuccess(t *testing.T) {
	f := newFakeS3()
	e := eventHandler(f, "secret", staticDump([]byte("data")))
	raw, _ := json.Marshal(httpRequest(map[string]string{"x-api-key": "secret"}, nil))

	out, err := e.Dispatch(context.Background(), raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := out.(events.APIGatewayV2HTTPResponse)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(resp.Body, `"status":"ok"`) {
		t.Errorf("body missing ok status: %s", resp.Body)
	}
}

func TestDispatchHTTPUnauthorized(t *testing.T) {
	e := eventHandler(newFakeS3(), "secret", staticDump([]byte("data")))
	raw, _ := json.Marshal(httpRequest(map[string]string{"x-api-key": "wrong"}, nil))

	out, _ := e.Dispatch(context.Background(), raw)
	resp := out.(events.APIGatewayV2HTTPResponse)
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestDispatchHTTPBackupError(t *testing.T) {
	e := eventHandler(newFakeS3(), "secret", failingDump(errors.New("boom")))
	raw, _ := json.Marshal(httpRequest(map[string]string{"x-api-key": "secret"}, nil))

	out, _ := e.Dispatch(context.Background(), raw)
	resp := out.(events.APIGatewayV2HTTPResponse)
	if resp.StatusCode != 500 {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestDispatchScheduled(t *testing.T) {
	f := newFakeS3()
	e := eventHandler(f, "secret", staticDump([]byte("scheduled-data")))
	// An EventBridge-style payload has no RequestContext.HTTP.Method.
	raw := json.RawMessage(`{"source":"aws.events","detail-type":"Scheduled Event"}`)

	out, err := e.Dispatch(context.Background(), raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != nil {
		t.Errorf("scheduled dispatch should return nil response, got %v", out)
	}
	if _, ok := f.objects["daily/2026-05-27-backup.sql"]; !ok {
		t.Error("expected scheduled run to create a daily backup")
	}
}

func TestJSONResponse(t *testing.T) {
	resp := jsonResponse(201, map[string]string{"hello": "world"})
	if resp.StatusCode != 201 {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}
	if resp.Headers["Content-Type"] != "application/json" {
		t.Errorf("content-type = %q", resp.Headers["Content-Type"])
	}
	if resp.Body != `{"hello":"world"}` {
		t.Errorf("body = %q", resp.Body)
	}
}
