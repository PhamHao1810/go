// Package service chứa use case (business logic). Chỉ biết domain và các interface do chính nó định nghĩa;
// không biết Postgres, Redis hay HTTP là gì → test được bằng bản giả, đổi DB không phải sửa ở đây.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"

	"golearn/04-products/internal/domain"
)

// Mọi đảm bảo concurrency (không bán âm kho, không ghi đè nhau) nằm trong câu SQL nguyên tử của repository,
// KHÔNG dùng mutex trong Go: mutex chỉ khoá được 1 process, còn ở đây nhiều instance chạy sau load balancer.
type ProductService struct {
	repo   ProductRepository
	cache  ProductCache
	assets AssetClaimer
	log    *slog.Logger
	sf     singleflight.Group
}

func NewProductService(repo ProductRepository, cache ProductCache, assets AssetClaimer, log *slog.Logger) *ProductService {
	return &ProductService{repo: repo, cache: cache, assets: assets, log: log}
}

func (s *ProductService) Create(ctx context.Context, p domain.Product) (domain.Product, error) {
	if err := p.Validate(); err != nil {
		return domain.Product{}, err
	}
	return s.repo.Create(ctx, p)
}

// Get: cache-aside + singleflight. 10k request cùng lúc vào 1 sản phẩm hot lúc cache vừa hết hạn
// chỉ tạo ra 1 query DB (chống cache stampede).
func (s *ProductService) Get(ctx context.Context, id int64) (domain.Product, error) {
	if p, ok, err := s.cache.Get(ctx, id); err != nil {
		s.log.WarnContext(ctx, "cache get failed", "err", err) // Redis lỗi thì vẫn phục vụ từ DB
	} else if ok {
		return p, nil
	}
	v, err, _ := s.sf.Do(strconv.FormatInt(id, 10), func() (any, error) {
		// Tách khỏi ctx của request đầu tiên: client đó ngắt kết nối thì các request chờ chung không lỗi theo.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if p, ok, err := s.cache.Get(ctx, id); err == nil && ok { // lượt trước vừa ghi cache (xem List)
			return p, nil
		}
		p, err := s.repo.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := s.cache.Set(ctx, p); err != nil {
			s.log.WarnContext(ctx, "cache set failed", "err", err)
		}
		return p, nil
	})
	if err != nil {
		return domain.Product{}, err
	}
	return v.(domain.Product), nil
}

// List: cache rất ngắn (rediscache.listTTL) + singleflight theo bộ lọc. Trang/từ khoá phổ biến bị hàng nghìn người
// xem cùng lúc chỉ tốn 1 truy vấn DB mỗi vài giây.
func (s *ProductService) List(ctx context.Context, f domain.ListFilter) (domain.Page, error) {
	if err := f.Normalize(); err != nil {
		return domain.Page{}, err
	}
	b, _ := json.Marshal(f) // sau Normalize: cùng bộ lọc → cùng key
	sum := sha256.Sum256(b)
	key := hex.EncodeToString(sum[:16])
	if p, ok, err := s.cache.GetList(ctx, key); err != nil {
		s.log.WarnContext(ctx, "list cache get failed", "err", err) // Redis lỗi thì vẫn phục vụ từ DB
	} else if ok {
		return p, nil
	}
	v, err, _ := s.sf.Do("list:"+key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		// Kiểm tra lại: lượt trước có thể vừa xong và ghi cache sau khi request này thấy cache trống.
		if p, ok, err := s.cache.GetList(ctx, key); err == nil && ok {
			return p, nil
		}
		p, err := s.repo.List(ctx, f)
		if err != nil {
			return nil, err
		}
		if err := s.cache.SetList(ctx, key, p); err != nil {
			s.log.WarnContext(ctx, "list cache set failed", "err", err)
		}
		return p, nil
	})
	if err != nil {
		return domain.Page{}, err
	}
	return v.(domain.Page), nil
}

func (s *ProductService) Update(ctx context.Context, p domain.Product) (domain.Product, error) {
	var v domain.Validator
	v.Check(p.Version >= 1, "version", "required: the version you last read")
	if err := v.Err(); err != nil {
		return domain.Product{}, err
	}
	if err := p.Validate(); err != nil {
		return domain.Product{}, err
	}
	p, err := s.repo.Update(ctx, p)
	if err == nil {
		s.invalidate(ctx, p.ID)
	}
	return p, err
}

func (s *ProductService) Delete(ctx context.Context, id int64) error {
	err := s.repo.Delete(ctx, id)
	if err == nil {
		s.invalidate(ctx, id)
	}
	return err
}

func (s *ProductService) Purchase(ctx context.Context, id, qty int64) (domain.Product, error) {
	var v domain.Validator
	v.Check(qty >= 1 && qty <= 1000, "quantity", "must be between 1 and 1000")
	if err := v.Err(); err != nil {
		return domain.Product{}, err
	}
	p, err := s.repo.Purchase(ctx, id, qty)
	if err == nil {
		s.invalidate(ctx, id)
	}
	return p, err
}

// AddImage gắn 1 file actor đã upload vào sản phẩm. Claim kiểm tra file thật sự đã lên storage, đúng loại,
// và đúng là của actor (không gắn được file người khác upload).
func (s *ProductService) AddImage(ctx context.Context, actor domain.Principal, productID, assetID int64) (domain.Product, error) {
	if _, err := s.assets.Claim(ctx, actor, assetID); err != nil {
		return domain.Product{}, err
	}
	if err := s.repo.AddImage(ctx, productID, assetID); err != nil {
		return domain.Product{}, err
	}
	s.invalidate(ctx, productID)
	return s.repo.Get(ctx, productID)
}

func (s *ProductService) RemoveImage(ctx context.Context, productID, assetID int64) (domain.Product, error) {
	if err := s.repo.RemoveImage(ctx, productID, assetID); err != nil {
		return domain.Product{}, err
	}
	s.invalidate(ctx, productID)
	return s.repo.Get(ctx, productID)
}

// Xoá cache SAU khi ghi DB (không Set giá trị mới: 2 update đồng thời có thể Set sai thứ tự).
// ponytail: còn khe hở nhỏ — request đọc DB trước update rồi Set cache sau khi xoá → cache cũ tối đa CACHE_TTL.
// Chấp nhận được vì Purchase/Update luôn kiểm tra trên DB; cần chặt hơn thì dùng delayed double-delete hoặc CDC.
func (s *ProductService) invalidate(ctx context.Context, id int64) {
	if err := s.cache.Delete(ctx, id); err != nil {
		s.log.WarnContext(ctx, "cache delete failed", "err", err, "id", id)
	}
}
