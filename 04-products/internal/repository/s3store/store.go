// Package s3store lưu file trên dịch vụ tương thích S3: SeaweedFS (chạy cùng máy, xem deploy/vps), AWS S3, Cloudflare R2...
// Đổi nhà cung cấp chỉ cần đổi biến môi trường STORAGE_*.
package s3store

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golearn/04-products/internal/domain"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Config struct {
	Endpoint        string // API dùng để Stat/Get/Remove, vd "storage:9000"
	PresignEndpoint string // host client nhìn thấy (upload_url ký theo host này), vd "localhost:9000" hoặc "https://shop.vn"; rỗng = Endpoint
	Region          string
	AccessKey       string
	SecretKey       string
	Bucket          string
	UseSSL          bool
	PublicURL       string // URL công khai của bucket/CDN, vd "https://img.shop.vn"
}

type Store struct {
	api, presign *minio.Client
	bucket       string
	publicURL    string
}

func New(cfg Config) (*Store, error) {
	newClient := func(endpoint string, secure bool) (*minio.Client, error) {
		return minio.New(endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
			Secure: secure,
			Region: cfg.Region, // có region thì ký URL không cần gọi mạng hỏi region của bucket
		})
	}
	api, err := newClient(cfg.Endpoint, cfg.UseSSL)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	presign := api
	if cfg.PresignEndpoint != "" && cfg.PresignEndpoint != cfg.Endpoint {
		// "https://shop.vn": API gọi storage bằng http trong mạng nội bộ, client upload qua https của reverse proxy.
		host, secure := cfg.PresignEndpoint, cfg.UseSSL
		if h, ok := strings.CutPrefix(host, "https://"); ok {
			host, secure = h, true
		} else if h, ok := strings.CutPrefix(host, "http://"); ok {
			host, secure = h, false
		}
		if presign, err = newClient(host, secure); err != nil {
			return nil, fmt.Errorf("storage presign: %w", err)
		}
	}
	return &Store{api: api, presign: presign, bucket: cfg.Bucket, publicURL: strings.TrimRight(cfg.PublicURL, "/")}, nil
}

// PresignUpload tạo URL cho client PUT file thẳng lên storage (không đi qua API → không tốn băng thông/CPU của API).
// Content-Type và Content-Length được KÝ vào URL: client gửi khác loại hoặc khác kích thước → storage từ chối.
func (s *Store) PresignUpload(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (domain.Upload, error) {
	h := http.Header{}
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	u, err := s.presign.PresignHeader(ctx, http.MethodPut, s.bucket, key, ttl, nil, h)
	if err != nil {
		return domain.Upload{}, err
	}
	// Content-Length trình duyệt/HTTP client tự đặt theo body; client chỉ cần tự đặt Content-Type.
	return domain.Upload{Method: http.MethodPut, URL: u.String(), Headers: map[string]string{"Content-Type": contentType}}, nil
}

func (s *Store) Stat(ctx context.Context, key string) (size int64, contentType string, found bool, err error) {
	info, err := s.api.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return 0, "", false, nil
		}
		return 0, "", false, err
	}
	return info.Size, info.ContentType, true, nil
}

// ReadHead đọc n byte đầu file (để kiểm tra "chữ ký" định dạng thật, không tin Content-Type client khai).
func (s *Store) ReadHead(ctx context.Context, key string, n int64) ([]byte, error) {
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(0, n-1); err != nil {
		return nil, err
	}
	obj, err := s.api.GetObject(ctx, s.bucket, key, opts)
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	return io.ReadAll(io.LimitReader(obj, n))
}

func (s *Store) Remove(ctx context.Context, key string) error {
	return s.api.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *Store) URL(key string) string { return s.publicURL + "/" + key }
