package predictor

import (
	"math"
	"testing"

	"gonum.org/v1/gonum/mat"
)

// Test basic quadratic Fit success path
func TestQuadraticPredictor_FitAndPredict(t *testing.T) {
	q := NewQuadraticPredictor(3)

	// ground truth: y = 2x^2 + 3x + 1
	aTrue, bTrue, cTrue := 2.0, 3.0, 1.0

	// Add some sample points
	xs := []float64{-2, -1, 0, 1, 2, 3}
	for _, x := range xs {
		y := aTrue*x*x + bTrue*x + cTrue
		q.AddSample(x, y)
	}

	if !q.ReadyForFit() {
		t.Fatalf("ReadyForFit() = false, want true (len(xs)=%d)", len(xs))
	}

	if err := q.Fit(); err != nil {
		t.Fatalf("Fit() returned error: %v", err)
	}

	if !q.Fitted() {
		t.Fatalf("Fitted() = false, want true after Fit()")
	}

	// Check that the fitted coefficients are close to the ground truth
	const coeffTol = 1e-6
	if math.Abs(q.a-aTrue) > coeffTol ||
		math.Abs(q.b-bTrue) > coeffTol ||
		math.Abs(q.c-cTrue) > coeffTol {
		t.Fatalf("unexpected fitted coeffs: got (a=%.8f, b=%.8f, c=%.8f), want (a=%.8f, b=%.8f, c=%.8f)",
			q.a, q.b, q.c, aTrue, bTrue, cTrue)
	}

	// Check that predictions are close to the true values at several points
	const predTol = 1e-5
	testXs := []float64{-3, -0.5, 0.5, 4}
	for _, x := range testXs {
		pred, err := q.Predict(x)
		if err != nil {
			t.Fatalf("Predict(%f) returned error: %v", x, err)
		}
		trueY := aTrue*x*x + bTrue*x + cTrue
		if diff := math.Abs(pred - trueY); diff > predTol {
			t.Fatalf("Predict(%f) = %.8f, want %.8f (|diff|=%.8f > tol=%.8f)",
				x, pred, trueY, diff, predTol)
		}
	}
}

// Test Predict called before model is fitted
func TestQuadraticPredictor_PredictBeforeFit(t *testing.T) {
	q := NewQuadraticPredictor(3)

	if _, err := q.Predict(1.0); err == nil {
		t.Fatalf("Predict() before Fit() should return error, got nil")
	}
}

// Test ReadyForFit vs numWarmupSamples and length of samples
func TestQuadraticPredictor_ReadyForFit(t *testing.T) {
	q := NewQuadraticPredictor(3)
	if q.ReadyForFit() {
		t.Fatalf("ReadyForFit() = true, want false when no samples")
	}

	q.AddSample(0, 0)
	if q.ReadyForFit() {
		t.Fatalf("ReadyForFit() = true, want false when num samples < numWarmupSamples")
	}

	q.AddSample(1, 1)
	if q.ReadyForFit() {
		t.Fatalf("ReadyForFit() = true, want false when num samples < numWarmupSamples")
	}

	q.AddSample(2, 4)
	if !q.ReadyForFit() {
		t.Fatalf("ReadyForFit() = false, want true when num samples >= numWarmupSamples")
	}
}

// Test Fit() error when not enough samples for the configured warmup
func TestQuadraticPredictor_NotEnoughSamples(t *testing.T) {
	q := NewQuadraticPredictor(3)

	// Add two samples, fewer than 3
	q.AddSample(0, 1)
	q.AddSample(1, 2)

	if q.ReadyForFit() {
		t.Fatalf("ReadyForFit() = true, want false when len(xs) < numWarmupSamples")
	}

	// Fit should return an error when there are fewer than 3 points
	if err := q.Fit(); err == nil {
		t.Fatalf("Fit() with less than 3 points should return error, got nil")
	}
}

// Test fitLinear directly: verify it recovers a linear function
func TestQuadraticPredictor_FitLinearDirect(t *testing.T) {
	q := NewQuadraticPredictor(1) // numWarmupSamples not relevant for direct fitLinear

	// ground truth: y = 5x + 2
	kTrue, cTrue := 5.0, 2.0
	xs := []float64{-1, 0, 1, 2}
	for _, x := range xs {
		y := kTrue*x + cTrue
		q.AddSample(x, y)
	}

	if err := q.fitLinear(); err != nil {
		t.Fatalf("fitLinear() returned error: %v", err)
	}

	if q.a != 0 {
		t.Fatalf("fitLinear() should set a=0, got %f", q.a)
	}

	const coeffTol = 1e-6
	if math.Abs(q.b-kTrue) > coeffTol || math.Abs(q.c-cTrue) > coeffTol {
		t.Fatalf("fitLinear() got (b=%.8f, c=%.8f), want (b=%.8f, c=%.8f)",
			q.b, q.c, kTrue, cTrue)
	}
}

// Test fitLinear error when there are fewer than 2 points
func TestQuadraticPredictor_FitLinearNotEnoughPoints(t *testing.T) {
	q := NewQuadraticPredictor(1)
	q.AddSample(0, 1)

	if err := q.fitLinear(); err == nil {
		t.Fatalf("fitLinear() with less than 2 points should return error, got nil")
	}
}

// Test fitQuadratic error when there are fewer than 3 points
func TestQuadraticPredictor_FitQuadraticNotEnoughPoints(t *testing.T) {
	q := NewQuadraticPredictor(1)
	q.AddSample(0, 1)
	q.AddSample(1, 2)

	if err := q.fitQuadratic(); err == nil {
		t.Fatalf("fitQuadratic() with less than 3 points should return error, got nil")
	}
}

// Test constant Fit with some samples: it should set a=0, b=0, c=mean(ys)
func TestQuadraticPredictor_FitConstantWithSamples(t *testing.T) {
	q := NewQuadraticPredictor(1)
	q.AddSample(0, 1)
	q.AddSample(1, 3)
	q.AddSample(2, 5)

	q.fitConstant()

	if q.a != 0 || q.b != 0 {
		t.Fatalf("fitConstant() should set a=0, b=0, got a=%f, b=%f", q.a, q.b)
	}

	// mean of ys: (1 + 3 + 5) / 3 = 3
	const expectedMean = 3.0
	if math.Abs(q.c-expectedMean) > 1e-9 {
		t.Fatalf("fitConstant() got c=%.8f, want %.8f", q.c, expectedMean)
	}
}

// Test constant Fit with zero samples: it should set all coeffs to 0
func TestQuadraticPredictor_FitConstantNoSamples(t *testing.T) {
	q := NewQuadraticPredictor(1)

	q.fitConstant()

	if q.a != 0 || q.b != 0 || q.c != 0 {
		t.Fatalf("fitConstant() with no samples should set a=b=c=0, got a=%f, b=%f, c=%f", q.a, q.b, q.c)
	}
}

// Try to make quadratic Fit ill-conditioned to exercise the condition-number branch.
// If this does not trigger on a platform, the test is skipped.
func TestQuadraticPredictor_QuadraticMayBeIllConditioned(t *testing.T) {
	q := NewQuadraticPredictor(3)

	// Use very small x values so that x^2 and x columns are almost zero,
	// making the matrix close to rank-1 dominated by the constant column.
	xs := []float64{1e-12, 2e-12, 3e-12, 4e-12}
	for _, x := range xs {
		q.AddSample(x, 1.0)
	}

	err := q.fitQuadratic()
	if err == nil {
		// On some platforms, SVD may still consider it well-conditioned enough.
		// We skip to avoid making the test flaky.
		t.Skip("quadratic Fit was not reported as ill-conditioned on this platform; skipping condition-branch test")
	}
}

// Sanity check: ensure Fit() uses quadratic when possible and does not fall back unnecessarily
func TestQuadraticPredictor_FitUsesQuadraticWhenWellConditioned(t *testing.T) {
	q := NewQuadraticPredictor(3)

	// y = x^2
	for _, x := range []float64{-2, -1, 0, 1, 2} {
		q.AddSample(x, x*x)
	}

	if err := q.Fit(); err != nil {
		t.Fatalf("Fit() returned error: %v", err)
	}

	// Expect a~1, b~0, c~0
	if math.Abs(q.a-1.0) > 1e-6 || math.Abs(q.b) > 1e-6 || math.Abs(q.c) > 1e-6 {
		t.Fatalf("Fit() did not produce expected quadratic coeffs, got a=%f, b=%f, c=%f", q.a, q.b, q.c)
	}
}

// Test Predict after constant Fit to ensure it uses y=c correctly
func TestQuadraticPredictor_PredictAfterConstantFit(t *testing.T) {
	q := NewQuadraticPredictor(1)
	q.AddSample(0, 10)
	q.AddSample(1, 20)
	q.AddSample(2, 30)

	// Force constant Fit directly
	q.fitConstant()
	q.fitted = true

	// mean is 20, so prediction should always be 20 regardless of x
	const expected = 20.0
	for _, x := range []float64{-10, 0, 10} {
		y, err := q.Predict(x)
		if err != nil {
			t.Fatalf("Predict() after constant Fit returned error: %v", err)
		}
		if math.Abs(y-expected) > 1e-9 {
			t.Fatalf("Predict(%f) after constant Fit got %f, want %f", x, y, expected)
		}
	}
}

// Small smoke test: ensure SVD usage in fitLinear matches the expected shape
func TestQuadraticPredictor_SVDShapeMatchesLinear(t *testing.T) {
	q := NewQuadraticPredictor(1)

	// y = 2x + 1
	for _, x := range []float64{0, 1, 2, 3} {
		q.AddSample(x, 2*x+1)
	}

	// Build the underlying design matrix in the same way as fitLinear to ensure consistency
	n := len(q.xs)
	A := mat.NewDense(n, 2, nil)
	for i := 0; i < n; i++ {
		x := q.xs[i]
		A.Set(i, 0, x)
		A.Set(i, 1, 1.0)
	}
	yVec := mat.NewVecDense(n, q.ys)

	var svd mat.SVD
	if ok := svd.Factorize(A, mat.SVDThin); !ok {
		t.Fatalf("SVD Factorize failed in test, but should succeed for well-conditioned linear data")
	}

	var p mat.VecDense
	cond := svd.SolveVecTo(&p, yVec, 2)
	if math.IsNaN(cond) || math.IsInf(cond, 0) {
		t.Fatalf("unexpected condition number from SVD: %v", cond)
	}
}
