package postgres

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golearn/04-products/internal/domain"
)

// Search chỉ chấm điểm tối đa ngần này dòng khớp: từ phổ biến ("nam" khớp 40k dòng) không làm DB tốn CPU
// chấm điểm cả 40k dòng (đo được: 21ms → 5ms; load test: Postgres hết sạch CPU vì search).
// ponytail: với từ quá phổ biến, kết quả chỉ xếp hạng trong 1000 dòng đó; cần chuẩn hơn thì dùng Meilisearch/Elasticsearch.
const maxRanked = 1000

// keysets: cột dùng để phân trang keyset cho từng kiểu sort (luôn kèm id để thứ tự là duy nhất).
var keysets = map[domain.Sort]struct {
	col  string
	desc bool
}{
	domain.SortNewest:    {"created_at", true},
	domain.SortOldest:    {"created_at", false},
	domain.SortPriceAsc:  {"price", false},
	domain.SortPriceDesc: {"price", true},
}

// cursor được base64 hoá thành chuỗi mờ: client chỉ việc gửi lại, không cần (và không nên) hiểu nội dung.
type cursor struct {
	Sort   string `json:"s"`
	Value  string `json:"v,omitempty"`  // giá trị cột sort của dòng cuối trang trước
	ID     int64  `json:"id,omitempty"` // id của dòng cuối trang trước
	Offset int    `json:"o,omitempty"`  // chỉ dùng cho sort=relevance (đã bị chặn ≤ maxRanked)
}

func encodeCursor(c cursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string, sort domain.Sort) (cursor, error) {
	var c cursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(b, &c) != nil || c.Sort != string(sort) || c.Offset < 0 || c.Offset >= maxRanked {
		return cursor{}, domain.ErrInvalidCursor
	}
	return c, nil
}

type listQuery struct {
	sql   string
	args  []any
	empty bool                        // q không có chữ nào tìm được → trả rỗng, khỏi query
	next  func(domain.Product) string // tạo cursor cho trang sau từ dòng cuối trang này
}

// buildListQuery ghép câu SQL từ filter. Giá trị người dùng luôn đi qua tham số ($1, $2...);
// tên cột chỉ lấy từ map keysets cố định → không có SQL injection.
func buildListQuery(f domain.ListFilter) (listQuery, error) {
	var (
		where []string
		args  []any
	)
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}

	var tsqArg string
	if f.Query != "" {
		tsq := prefixQuery(f.Query)
		if tsq == "" {
			return listQuery{empty: true}, nil
		}
		tsqArg = arg(tsq)
		where = append(where, "search @@ to_tsquery('simple', "+tsqArg+")")
	}
	if f.MinPrice != nil {
		where = append(where, "price >= "+arg(*f.MinPrice))
	}
	if f.MaxPrice != nil {
		where = append(where, "price <= "+arg(*f.MaxPrice))
	}
	if f.InStock {
		where = append(where, "stock > 0")
	}
	if f.BrandID != nil {
		where = append(where, "brand_id = "+arg(*f.BrandID))
	}
	// Danh mục gồm cả cây con: path = path của nó hoặc bắt đầu bằng "path/".
	subtree := ""
	if f.CategoryID != nil {
		subtree = `SELECT c.id AS cat_id FROM categories c, categories root
			WHERE root.id = ` + arg(*f.CategoryID) + ` AND (c.path = root.path OR c.path LIKE root.path || '/%')`
		if f.Sort == domain.SortRelevance { // search trong danh mục: lọc bình thường (đã giới hạn maxRanked dòng)
			where = append(where, `category_id IN (SELECT cat_id FROM (`+subtree+`) s)`)
		}
	}

	var c cursor
	if f.Cursor != "" {
		var err error
		if c, err = decodeCursor(f.Cursor, f.Sort); err != nil {
			return listQuery{}, err
		}
	}

	if f.Sort == domain.SortRelevance {
		sql := fmt.Sprintf(`SELECT %s FROM (SELECT %s, search FROM products WHERE %s LIMIT %d) m
			ORDER BY ts_rank(search, to_tsquery('simple', %s)) DESC, id LIMIT %s OFFSET %s`,
			cols, cols, strings.Join(where, " AND "), maxRanked, tsqArg, arg(f.Limit+1), arg(c.Offset))
		next := func(domain.Product) string {
			if c.Offset+f.Limit >= maxRanked {
				return ""
			}
			return encodeCursor(cursor{Sort: string(f.Sort), Offset: c.Offset + f.Limit})
		}
		return listQuery{sql: sql, args: args, next: next}, nil
	}

	ks := keysets[f.Sort]
	if f.Cursor != "" {
		var v any
		var err error
		if ks.col == "price" {
			v, err = strconv.ParseInt(c.Value, 10, 64)
		} else {
			v, err = time.Parse(time.RFC3339Nano, c.Value)
		}
		if err != nil {
			return listQuery{}, domain.ErrInvalidCursor
		}
		op := ">"
		if ks.desc {
			op = "<"
		}
		where = append(where, fmt.Sprintf("(%s, id) %s (%s, %s)", ks.col, op, arg(v), arg(c.ID)))
	}
	dir := "ASC"
	if ks.desc {
		dir = "DESC"
	}
	order := fmt.Sprintf(` ORDER BY %s %s, id %s LIMIT %s`, ks.col, dir, dir, arg(f.Limit+1))
	var sql string
	if subtree == "" {
		sql = `SELECT ` + cols + ` FROM products`
		if len(where) > 0 {
			sql += ` WHERE ` + strings.Join(where, " AND ")
		}
		sql += order
	} else {
		// Duyệt danh mục cha (vd 75.000 sản phẩm trong 30 danh mục con): KHÔNG lọc "category_id IN (...)" rồi sắp
		// cả 75.000 dòng. Mỗi danh mục con lấy N dòng đầu theo index (category_id, cột sort, id) đã sắp sẵn, rồi
		// trộn ~30 x N dòng. Đo trên 1 triệu sản phẩm: 148ms → 4ms; load test trước khi sửa: Postgres primary
		// hết sạch CPU chỉ vì truy vấn này.
		where = append([]string{"category_id = s.cat_id"}, where...)
		sql = `SELECT ` + cols + ` FROM (` + subtree + `) s CROSS JOIN LATERAL (SELECT ` + cols + ` FROM products WHERE ` +
			strings.Join(where, " AND ") + order + `) p` + order
	}

	next := func(last domain.Product) string {
		v := last.CreatedAt.Format(time.RFC3339Nano)
		if ks.col == "price" {
			v = strconv.FormatInt(last.Price, 10)
		}
		return encodeCursor(cursor{Sort: string(f.Sort), Value: v, ID: last.ID})
	}
	return listQuery{sql: sql, args: args, next: next}, nil
}

// prefixQuery biến "Áo  thun!" thành "Áo:* & thun:*" (tìm theo tiền tố từng từ). Chỉ giữ chữ/số nên
// người dùng không chèn được cú pháp tsquery (& | ! ( ) ...) để làm lỗi câu query.
func prefixQuery(q string) string {
	words := strings.FieldsFunc(q, func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsDigit(c) && !unicode.Is(unicode.Mn, c)
	})
	for i, w := range words {
		words[i] = w + ":*"
	}
	return strings.Join(words, " & ")
}
