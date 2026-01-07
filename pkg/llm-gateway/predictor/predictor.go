package predictor

import (
	"errors"
	"fmt"
	"math"

	"gonum.org/v1/gonum/mat"
	"k8s.io/klog/v2"
)

type QuadraticPredictor struct {
	xs []float64
	ys []float64

	a, b, c float64
	fitted  bool

	numWarmupSamples int
}

func NewQuadraticPredictor(numWarmupSamples int) *QuadraticPredictor {
	return &QuadraticPredictor{
		numWarmupSamples: numWarmupSamples,
	}
}

func (q *QuadraticPredictor) AddSample(x, y float64) {
	klog.V(4).Infof("[QuadraticPredictor] Adding sample (%f, %f)", x, y)
	q.xs = append(q.xs, x)
	q.ys = append(q.ys, y)
	q.fitted = false
}

func (q *QuadraticPredictor) ReadyForFit() bool {
	return len(q.xs) >= q.numWarmupSamples
}

func (q *QuadraticPredictor) Fitted() bool {
	return q.fitted
}

func (q *QuadraticPredictor) Fit() error {
	if !q.ReadyForFit() {
		return errors.New("need at least 3 points for quadratic Fit")
	}

	klog.V(4).Infof("[QuadraticPredictor] Fitting quadratic predictor with %d samples", len(q.xs))

	// 1. Try quadratic Fit: y = a*x^2 + b*x + c
	if err := q.fitQuadratic(); err == nil {
		klog.V(4).Infof("[QuadraticPredictor] Quadratic Fit succeeded: a=%f, b=%f, c=%f", q.a, q.b, q.c)
		q.fitted = true
		return nil
	} else {
		klog.Warningf("[QuadraticPredictor] Quadratic Fit failed, trying linear fallback: %v", err)
	}

	// 2. Fallback: linear Fit: y = k*x + c
	if err := q.fitLinear(); err == nil {
		klog.V(4).Infof("[QuadraticPredictor] Linear Fit succeeded (fallback): a=%f, b=%f, c=%f", q.a, q.b, q.c)
		q.fitted = true
		return nil
	} else {
		klog.Warningf("[QuadraticPredictor] Linear Fit failed, falling back to constant model: %v", err)
	}

	// 3. Last resort: constant model y = mean(y)
	q.fitConstant()
	klog.V(4).Infof("[QuadraticPredictor] Constant Fit succeeded (last resort): a=%f, b=%f, c=%f", q.a, q.b, q.c)
	q.fitted = true
	return nil
}

// Quadratic: y = a*x^2 + b*x + c
func (q *QuadraticPredictor) fitQuadratic() error {
	n := len(q.xs)
	if n < 3 {
		return errors.New("need at least 3 points for quadratic Fit")
	}

	// A: n×3, each row is [x^2, x, 1]
	A := mat.NewDense(n, 3, nil)
	for i := 0; i < n; i++ {
		x := q.xs[i]
		A.Set(i, 0, x*x)
		A.Set(i, 1, x)
		A.Set(i, 2, 1.0)
	}
	y := mat.NewVecDense(n, q.ys)

	var svd mat.SVD
	ok := svd.Factorize(A, mat.SVDThin)
	if !ok {
		return errors.New("svd factorization failed for quadratic Fit")
	}

	var p mat.VecDense

	// rank=0 → let SVD choose effective rank automatically
	cond := svd.SolveVecTo(&p, y, 3)

	// If cond is too large or NaN/Inf, treat as failure (ill-conditioned)
	const condThreshold = 1e8
	if cond > condThreshold || math.IsNaN(cond) || math.IsInf(cond, 0) {
		return fmt.Errorf("quadratic Fit ill-conditioned: cond=%g", cond)
	}

	q.a = p.AtVec(0)
	q.b = p.AtVec(1)
	q.c = p.AtVec(2)
	return nil
}

// Linear: y = k*x + c  (encoded as a=0, b=k, c=c0)
func (q *QuadraticPredictor) fitLinear() error {
	n := len(q.xs)
	if n < 2 {
		return errors.New("need at least 2 points for linear Fit")
	}

	// A: n×2, each row is [x, 1]
	A := mat.NewDense(n, 2, nil)
	for i := 0; i < n; i++ {
		x := q.xs[i]
		A.Set(i, 0, x)
		A.Set(i, 1, 1.0)
	}
	y := mat.NewVecDense(n, q.ys)

	var svd mat.SVD
	ok := svd.Factorize(A, mat.SVDThin)
	if !ok {
		return errors.New("svd factorization failed for linear Fit")
	}

	var p mat.VecDense
	cond := svd.SolveVecTo(&p, y, 2)

	const condThreshold = 1e12
	if cond > condThreshold || math.IsNaN(cond) || math.IsInf(cond, 0) {
		return fmt.Errorf("linear Fit ill-conditioned: cond=%g", cond)
	}

	k := p.AtVec(0)
	c0 := p.AtVec(1)

	// encode y = k*x + c0 as a=0, b=k, c=c0
	q.a = 0.0
	q.b = k
	q.c = c0
	return nil
}

// Constant: y = mean(y)  (encoded as a=0, b=0, c=mean)
func (q *QuadraticPredictor) fitConstant() {
	n := len(q.ys)
	if n == 0 {
		q.a, q.b, q.c = 0, 0, 0
		return
	}
	sum := 0.0
	for _, v := range q.ys {
		sum += v
	}
	mean := sum / float64(n)

	q.a = 0.0
	q.b = 0.0
	q.c = mean
}

// Predict returns y = a*x^2 + b*x + c for a given x
func (q *QuadraticPredictor) Predict(x float64) (float64, error) {
	if !q.fitted {
		return 0, errors.New("model not fitted yet")
	}
	y := q.a*x*x + q.b*x + q.c
	klog.V(4).Infof("[QuadraticPredictor] Predicting y = %f for x = %f", y, x)
	return y, nil
}
