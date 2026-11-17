package s3store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Ký URL không gọi mạng (đã có Region), nên test chạy không cần storage thật.
func TestPresignEndpointScheme(t *testing.T) {
	for _, tc := range []struct{ presign, want string }{
		{"", "http://storage:9000/media/k.png?"},
		{"localhost:9000", "http://localhost:9000/media/k.png?"},
		{"https://shop.vn", "https://shop.vn/media/k.png?"},
	} {
		s, err := New(Config{Endpoint: "storage:9000", PresignEndpoint: tc.presign, Region: "us-east-1", AccessKey: "a", SecretKey: "b", Bucket: "media"})
		if err != nil {
			t.Fatal(err)
		}
		up, err := s.PresignUpload(context.Background(), "k.png", "image/png", 10, time.Minute)
		if err != nil || !strings.HasPrefix(up.URL, tc.want) || up.Method != "PUT" {
			t.Errorf("presign %q: url=%s err=%v, muốn bắt đầu bằng %s", tc.presign, up.URL, err, tc.want)
		}
	}
}
