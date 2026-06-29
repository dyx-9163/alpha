package apperror

import (
	"net/http"
	"testing"
)

func TestNewDefaultsStatusAndBuildsBody(t *testing.T) {
	err := New(0, "REQUEST_FAILED", "failed", map[string]any{"id": "1"})
	if err.Status != http.StatusInternalServerError {
		t.Fatalf("unexpected status: %d", err.Status)
	}
	if err.Error() != "failed" {
		t.Fatalf("unexpected error text: %s", err.Error())
	}
	if err.Body()["code"] != "REQUEST_FAILED" {
		t.Fatalf("unexpected body: %+v", err.Body())
	}
}
