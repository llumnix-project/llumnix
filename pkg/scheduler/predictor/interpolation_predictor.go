package predictor

import (
	"fmt"
	"math"

	"k8s.io/klog/v2"
)

const epsilon = 1e-10

type Point2D struct {
	a float64
	b float64
}

type InterpolationPredictor struct {
	data    map[Point2D]float64
	aValues map[float64]bool
	bValues map[float64]bool
}

func NewInterpolationPredictor() *InterpolationPredictor {
	return &InterpolationPredictor{
		data:    make(map[Point2D]float64),
		aValues: make(map[float64]bool),
		bValues: make(map[float64]bool),
	}
}

func (bi *InterpolationPredictor) AddSample(a, b, value float64) {
	bi.data[Point2D{a: a, b: b}] = value
	bi.aValues[a] = true
	bi.bValues[b] = true
}

// Predict If not found the bounding box, return 0 and error
func (bi *InterpolationPredictor) Predict(a, b float64) (float64, error) {
	if a == 0 && b == 0 {
		return 0, nil
	}

	a1, a2, b1, b2, err := bi.findBoundingBox(a, b)
	klog.V(4).Infof("InterpolationPredictor findBoundingBox: (%.2f, %.2f) -> (%.2f, %.2f, %.2f, %.2f)", a, b, a1, a2, b1, b2)

	if err != nil {
		return 0, err
	}

	z11, exist := bi.data[Point2D{a: a1, b: b1}]
	if !exist {
		return 0, fmt.Errorf("point (%.2f, %.2f) is outside the data range", a1, b1)
	}
	z12, exist := bi.data[Point2D{a: a1, b: b2}]
	if !exist {
		return 0, fmt.Errorf("point (%.2f, %.2f) is outside the data range", a1, b2)
	}
	z21, exist := bi.data[Point2D{a: a2, b: b1}]
	if !exist {
		return 0, fmt.Errorf("point (%.2f, %.2f) is outside the data range", a2, b1)
	}
	z22, exist := bi.data[Point2D{a: a2, b: b2}]
	if !exist {
		return 0, fmt.Errorf("point (%.2f, %.2f) is outside the data range", a2, b2)
	}

	if math.Abs(a2-a1) < epsilon && math.Abs(b2-b1) < epsilon {
		return z11, nil
	}

	if math.Abs(a2-a1) < epsilon {
		wb := (b - b1) / (b2 - b1)
		return (1-wb)*z11 + wb*z12, nil
	}

	if math.Abs(b2-b1) < epsilon {
		wa := (a - a1) / (a2 - a1)
		return (1-wa)*z11 + wa*z21, nil
	}

	wa := (a - a1) / (a2 - a1)
	wb := (b - b1) / (b2 - b1)

	result := (1-wa)*(1-wb)*z11 +
		(1-wa)*wb*z12 +
		wa*(1-wb)*z21 +
		wa*wb*z22

	return result, nil
}

func (bi *InterpolationPredictor) findBoundingBox(a, b float64) (a1, a2, b1, b2 float64, err error) {
	a1, a2 = math.Inf(-1), math.Inf(1)
	foundA := false
	for av := range bi.aValues {
		if av <= a && av > a1 {
			a1 = av
			foundA = true
		}
		if av >= a && av < a2 {
			a2 = av
			foundA = true
		}
	}

	b1, b2 = math.Inf(-1), math.Inf(1)
	foundB := false
	for bv := range bi.bValues {
		if bv <= b && bv > b1 {
			b1 = bv
			foundB = true
		}
		if bv >= b && bv < b2 {
			b2 = bv
			foundB = true
		}
	}

	if !foundA || !foundB {
		return 0, 0, 0, 0, fmt.Errorf("point (%.2f, %.2f) is outside the data range", a, b)
	}

	return a1, a2, b1, b2, nil
}
