package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golearn/04-products/internal/domain"
)

// catalogTTL: danh mục/hãng ít đổi, mỗi instance giữ trong RAM ngần này. Admin sửa thì instance nhận request
// thấy ngay; instance khác thấy sau tối đa catalogTTL.
const catalogTTL = 30 * time.Second

// CatalogService: danh mục cây + hãng. Đọc rất nhiều (mỗi trang danh mục, mỗi lần đồng bộ search) nên cache RAM.
type CatalogService struct {
	repo CatalogRepository
	log  *slog.Logger

	mu       sync.Mutex
	loadedAt time.Time
	cats     []domain.Category
	byID     map[int64]domain.Category
	brands   []domain.Brand
}

func NewCatalogService(repo CatalogRepository, log *slog.Logger) *CatalogService {
	return &CatalogService{repo: repo, log: log}
}

func (s *CatalogService) load(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.loadedAt) < catalogTTL {
		return nil
	}
	cats, err := s.repo.Categories(ctx)
	if err != nil {
		return err
	}
	brands, err := s.repo.Brands(ctx)
	if err != nil {
		return err
	}
	s.cats, s.brands, s.loadedAt = cats, brands, time.Now()
	s.byID = make(map[int64]domain.Category, len(cats))
	for _, c := range cats {
		s.byID[c.ID] = c
	}
	return nil
}

func (s *CatalogService) reset() {
	s.mu.Lock()
	s.loadedAt = time.Time{}
	s.mu.Unlock()
}

func (s *CatalogService) CategoryTree(ctx context.Context) ([]*domain.CategoryNode, error) {
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return domain.BuildCategoryTree(s.cats), nil
}

// CategoryIndex: map id → danh mục (bản chụp, an toàn để đọc song song).
func (s *CatalogService) CategoryIndex(ctx context.Context) (map[int64]domain.Category, error) {
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byID, nil // map không bị sửa sau khi tạo (load tạo map mới) nên trả thẳng được
}

func (s *CatalogService) Brands(ctx context.Context) ([]domain.Brand, error) {
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.brands, nil
}

func (s *CatalogService) CreateCategory(ctx context.Context, c domain.Category) (domain.Category, error) {
	c.Name = strings.TrimSpace(c.Name)
	if err := c.Validate(); err != nil {
		return domain.Category{}, err
	}
	out, err := s.repo.CreateCategory(ctx, c)
	s.reset()
	return out, err
}

func (s *CatalogService) UpdateCategory(ctx context.Context, c domain.Category) (domain.Category, error) {
	c.Name = strings.TrimSpace(c.Name)
	c.ParentID = nil // không đổi cha được; Validate bỏ qua
	if err := c.Validate(); err != nil {
		return domain.Category{}, err
	}
	out, err := s.repo.UpdateCategory(ctx, c)
	s.reset()
	return out, err
}

func (s *CatalogService) DeleteCategory(ctx context.Context, id int64) error {
	err := s.repo.DeleteCategory(ctx, id)
	s.reset()
	return err
}

func (s *CatalogService) CreateBrand(ctx context.Context, b domain.Brand) (domain.Brand, error) {
	b.Name = strings.TrimSpace(b.Name)
	if err := b.Validate(); err != nil {
		return domain.Brand{}, err
	}
	out, err := s.repo.CreateBrand(ctx, b)
	s.reset()
	return out, err
}

func (s *CatalogService) UpdateBrand(ctx context.Context, b domain.Brand) (domain.Brand, error) {
	b.Name = strings.TrimSpace(b.Name)
	if err := b.Validate(); err != nil {
		return domain.Brand{}, err
	}
	out, err := s.repo.UpdateBrand(ctx, b)
	s.reset()
	return out, err
}

func (s *CatalogService) DeleteBrand(ctx context.Context, id int64) error {
	err := s.repo.DeleteBrand(ctx, id)
	s.reset()
	return err
}
