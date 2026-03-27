package storage

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"astron-claw/backend/internal/config"
)

func TestS3PutObjectDisablesDefaultChecksumTrailers(t *testing.T) {
	var gotContentEncoding string
	var gotDecodedLength string
	var gotChecksumAlgorithm string
	var gotPayloadHash string
	var gotBody []byte

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentEncoding = r.Header.Get("Content-Encoding")
		gotDecodedLength = r.Header.Get("X-Amz-Decoded-Content-Length")
		gotChecksumAlgorithm = r.Header.Get("X-Amz-Sdk-Checksum-Algorithm")
		gotPayloadHash = r.Header.Get("X-Amz-Content-Sha256")

		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		_ = r.Body.Close()

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	storage := NewS3Storage(config.StorageConfig{
		Type:           "s3",
		Endpoint:       srv.URL,
		PublicEndpoint: srv.URL,
		AccessKey:      "test-access-key",
		SecretKey:      "test-secret-key",
		Bucket:         "test-bucket",
		Region:         "oss-cn-beijing",
	})
	storage.httpClient = srv.Client()
	if err := storage.Start(); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	const payload = "hello oss"
	if _, err := storage.PutObject("sample.txt", bytes.NewReader([]byte(payload)), "text/plain", int64(len(payload))); err != nil {
		t.Fatalf("PutObject returned error: %v", err)
	}

	if gotContentEncoding != "" {
		t.Fatalf("Content-Encoding = %q, want empty", gotContentEncoding)
	}
	if gotDecodedLength != "" {
		t.Fatalf("X-Amz-Decoded-Content-Length = %q, want empty", gotDecodedLength)
	}
	if gotChecksumAlgorithm != "" {
		t.Fatalf("X-Amz-Sdk-Checksum-Algorithm = %q, want empty", gotChecksumAlgorithm)
	}
	if gotPayloadHash == "STREAMING-UNSIGNED-PAYLOAD-TRAILER" {
		t.Fatalf("X-Amz-Content-Sha256 = %q, want non-streaming payload hash", gotPayloadHash)
	}
	if string(gotBody) != payload {
		t.Fatalf("request body = %q, want %q", string(gotBody), payload)
	}
}
