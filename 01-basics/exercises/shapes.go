package exercises

// Bài 4: Interface.
// Cài đặt Area (diện tích) và Perimeter (chu vi) cho Rectangle và Circle, rồi viết TotalArea.
// Để ý: không cần khai báo "implements Shape". Có đủ method là tự thỏa interface.
// Gợi ý: math.Pi
type Shape interface {
	Area() float64
	Perimeter() float64
}

type Rectangle struct{ Width, Height float64 }

type Circle struct{ Radius float64 }

func (r Rectangle) Area() float64      { return 0 } // TODO
func (r Rectangle) Perimeter() float64 { return 0 } // TODO
func (c Circle) Area() float64         { return 0 } // TODO
func (c Circle) Perimeter() float64    { return 0 } // TODO

// TotalArea trả về tổng diện tích của mọi hình.
func TotalArea(shapes []Shape) float64 {
	// TODO
	return 0
}
