package service

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWriteOpenAICompactSSEFailureMessageParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writeOpenAICompactSSEFailureMessageParam(ctx, 400, "basispoints_request_invalid", "cannot forward encrypted content", "input[103].content[1]")
	data := recorder.Body.String()
	parts := strings.SplitN(data, "data: ", 2)
	if len(parts) != 2 || !strings.HasPrefix(data, "event: response.failed\n") {
		t.Fatalf("missing response.failed event: %q", data)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(parts[1])), &event); err != nil {
		t.Fatal(err)
	}
	response, ok := event["response"].(map[string]any)
	if !ok {
		t.Fatalf("missing or invalid response object: %#v", event["response"])
	}
	errBody, ok := response["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing or invalid error object: %#v", response["error"])
	}
	if errBody["param"] != "input[103].content[1]" {
		t.Fatalf("missing safe param: %#v", errBody)
	}
}
