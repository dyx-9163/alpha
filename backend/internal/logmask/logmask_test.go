package logmask

import (
	"strings"
	"testing"
)

func TestMaskRedactsCommonSecretFormats(t *testing.T) {
	input := `password=secret123 token:abcd {"serverPassword":"root-pass","accessKey":"minio-key"}`

	got := Mask(input)

	for _, leaked := range []string{"secret123", "abcd", "root-pass", "minio-key"} {
		if contains(got, leaked) {
			t.Fatalf("expected %q to be redacted from %q", leaked, got)
		}
	}
	if !contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker in %q", got)
	}
}

func TestMaskRedactsBearerURLAndPrivateKey(t *testing.T) {
	input := "Authorization: Bearer abc.def.ghi\nGET /api?token=secret-token&name=a\n-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----"

	got := Mask(input)

	for _, leaked := range []string{"abc.def.ghi", "secret-token", "-----BEGIN PRIVATE KEY-----"} {
		if contains(got, leaked) {
			t.Fatalf("expected %q to be redacted from %q", leaked, got)
		}
	}
	if !contains(got, "Authorization: [REDACTED]") || !contains(got, "[REDACTED_PRIVATE_KEY]") {
		t.Fatalf("expected authorization and private key markers in %q", got)
	}
}

func contains(text, part string) bool {
	return strings.Contains(text, part)
}
