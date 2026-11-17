package exercises

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestShapes(t *testing.T) {
	tests := []struct {
		name        string
		s           Shape
		area, perim float64
	}{
		{"rectangle 3x4", Rectangle{Width: 3, Height: 4}, 12, 14},
		{"circle r=1", Circle{Radius: 1}, math.Pi, 2 * math.Pi},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Area(); !almostEqual(got, tt.area) {
				t.Errorf("Area() = %v, muốn %v", got, tt.area)
			}
			if got := tt.s.Perimeter(); !almostEqual(got, tt.perim) {
				t.Errorf("Perimeter() = %v, muốn %v", got, tt.perim)
			}
		})
	}
}

func TestTotalArea(t *testing.T) {
	got := TotalArea([]Shape{Rectangle{Width: 2, Height: 3}, Rectangle{Width: 1, Height: 1}})
	if !almostEqual(got, 7) {
		t.Errorf("TotalArea() = %v, muốn 7", got)
	}
}
