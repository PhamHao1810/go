package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Category: cây nhiều cấp. Path = id từ gốc xuống ("1/5/23"), dùng để lấy cả cây con.
type Category struct {
	ID        int64
	ParentID  *int64
	Name      string
	Slug      string
	Path      string
	Depth     int
	CreatedAt time.Time
}

// CategoryNode: 1 nút trong cây trả cho client.
type CategoryNode struct {
	Category
	Children []*CategoryNode
}

type Brand struct {
	ID        int64
	Name      string
	Slug      string
	CreatedAt time.Time
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (c Category) Validate() error {
	var v Validator
	v.Check(strings.TrimSpace(c.Name) != "" && utf8.RuneCountInString(c.Name) <= 100, "name", "required, max 100 characters")
	v.Check(slugRe.MatchString(c.Slug) && len(c.Slug) <= 120, "slug", "required, lowercase a-z 0-9 and '-', max 120")
	v.Check(c.ParentID == nil || *c.ParentID > 0, "parent_id", "must be a positive integer")
	return v.Err()
}

func (b Brand) Validate() error {
	var v Validator
	v.Check(strings.TrimSpace(b.Name) != "" && utf8.RuneCountInString(b.Name) <= 100, "name", "required, max 100 characters")
	v.Check(slugRe.MatchString(b.Slug) && len(b.Slug) <= 120, "slug", "required, lowercase a-z 0-9 and '-', max 120")
	return v.Err()
}

// BuildCategoryTree ghép danh sách phẳng (sắp theo depth) thành cây. Nút có cha không tồn tại thành gốc.
func BuildCategoryTree(list []Category) []*CategoryNode {
	nodes := make(map[int64]*CategoryNode, len(list))
	for _, c := range list {
		nodes[c.ID] = &CategoryNode{Category: c, Children: []*CategoryNode{}}
	}
	roots := []*CategoryNode{}
	for _, c := range list {
		n := nodes[c.ID]
		if c.ParentID != nil {
			if parent, ok := nodes[*c.ParentID]; ok {
				parent.Children = append(parent.Children, n)
				continue
			}
		}
		roots = append(roots, n)
	}
	return roots
}

// CategoryAncestors trả các danh mục từ gốc xuống tới id (gồm cả id), dựa trên Path. Thiếu nút → bỏ qua nút đó.
func CategoryAncestors(byID map[int64]Category, id int64) []Category {
	c, ok := byID[id]
	if !ok {
		return nil
	}
	parts := strings.Split(c.Path, "/")
	out := make([]Category, 0, len(parts))
	for _, s := range parts {
		var pid int64
		for _, ch := range s {
			pid = pid*10 + int64(ch-'0')
		}
		if a, ok := byID[pid]; ok {
			out = append(out, a)
		}
	}
	return out
}

var (
	ErrCategoryNotFound = &Error{Kind: KindNotFound, Code: "CATEGORY_NOT_FOUND", Message: "category not found"}
	ErrCategoryInUse    = &Error{Kind: KindConflict, Code: "CATEGORY_IN_USE", Message: "category still has child categories"}
	ErrBrandNotFound    = &Error{Kind: KindNotFound, Code: "BRAND_NOT_FOUND", Message: "brand not found"}
	ErrSlugTaken        = &Error{Kind: KindConflict, Code: "SLUG_TAKEN", Message: "name or slug already exists"}
)
