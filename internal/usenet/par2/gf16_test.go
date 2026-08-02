package par2

import (
	"math/rand"
	"testing"
)

// The field is the one place where a wrong answer is completely silent: every
// operation returns a plausible 16-bit number, and the first sign of trouble
// is a repaired file that does not play. So the field gets tested against its
// own algebra exhaustively rather than against a handful of examples.

func TestGFTablesRoundTrip(t *testing.T) {
	for x := 1; x < gfOrder; x++ {
		if got := gfExp[gfLog[uint16(x)]]; got != uint16(x) {
			t.Fatalf("gfExp[gfLog[%d]] = %d, want %d", x, got, x)
		}
	}
	for l := 0; l < gfLimit; l++ {
		if got := gfLog[gfExp[l]]; int(got) != l {
			t.Fatalf("gfLog[gfExp[%d]] = %d, want %d", l, got, l)
		}
		if gfExp[l] != gfExp[l+gfLimit] {
			t.Fatalf("gfExp is not duplicated at %d", l)
		}
	}
	// The generator is 2, so the table must start 1, 2, 4, 8 and wrap at
	// 2^16 into the reduction polynomial's low half.
	for i, want := range []uint16{1, 2, 4, 8, 16} {
		if gfExp[i] != want {
			t.Fatalf("gfExp[%d] = %d, want %d", i, gfExp[i], want)
		}
	}
	if gfExp[16] != gfPoly&0xFFFF {
		t.Fatalf("gfExp[16] = %d, want %d (the reduction polynomial's low half)", gfExp[16], gfPoly&0xFFFF)
	}
}

func TestGFInverseExhaustive(t *testing.T) {
	for x := 1; x < gfOrder; x++ {
		a := uint16(x)
		if got := gfMul(a, gfInv(a)); got != 1 {
			t.Fatalf("%d * inv(%d) = %d, want 1", a, a, got)
		}
	}
}

func TestGFMulDivIdentities(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200000; i++ {
		a := uint16(rng.Intn(gfOrder))
		b := uint16(rng.Intn(gfOrder))
		c := uint16(rng.Intn(gfOrder))

		if got := gfMul(a, 0); got != 0 {
			t.Fatalf("%d * 0 = %d, want 0", a, got)
		}
		if got := gfMul(a, 1); got != a {
			t.Fatalf("%d * 1 = %d, want %d", a, got, a)
		}
		if gfMul(a, b) != gfMul(b, a) {
			t.Fatalf("multiplication is not commutative at (%d, %d)", a, b)
		}
		if gfMul(gfMul(a, b), c) != gfMul(a, gfMul(b, c)) {
			t.Fatalf("multiplication is not associative at (%d, %d, %d)", a, b, c)
		}
		// Addition in this field is XOR, so distributivity is the identity
		// the whole recovery scheme rests on.
		if gfMul(a, b^c) != gfMul(a, b)^gfMul(a, c) {
			t.Fatalf("multiplication does not distribute at (%d, %d, %d)", a, b, c)
		}
		if b != 0 {
			if got := gfDiv(gfMul(a, b), b); got != a {
				t.Fatalf("(%d * %d) / %d = %d, want %d", a, b, b, got, a)
			}
			if got := gfMul(gfDiv(a, b), b); got != a {
				t.Fatalf("(%d / %d) * %d = %d, want %d", a, b, b, got, a)
			}
		}
	}
}

func TestGFDivByZeroPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("gfDiv by zero returned instead of panicking")
		}
	}()
	_ = gfDiv(5, 0)
}

func TestGFPow(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 2000; i++ {
		a := uint16(1 + rng.Intn(gfOrder-1))

		if got := gfPow(a, 0); got != 1 {
			t.Fatalf("%d^0 = %d, want 1", a, got)
		}
		// Every non-zero element's order divides the group order.
		if got := gfPow(a, gfLimit); got != 1 {
			t.Fatalf("%d^%d = %d, want 1", a, gfLimit, got)
		}

		// Against repeated multiplication, which is the definition.
		want := uint16(1)
		for e := uint32(0); e <= 40; e++ {
			if got := gfPow(a, e); got != want {
				t.Fatalf("%d^%d = %d, want %d", a, e, got, want)
			}
			want = gfMul(want, a)
		}
	}
	if got := gfPow(0, 5); got != 0 {
		t.Fatalf("0^5 = %d, want 0", got)
	}
}

func TestSliceConstants(t *testing.T) {
	// The first eleven constants are published in the specification. If these
	// ever drift, every recovery slice we compute against is being multiplied
	// by the wrong number.
	want := []uint16{2, 4, 16, 128, 256, 2048, 8192, 16384, 4107, 32856, 17132}
	for i, w := range want {
		got, ok := sliceConstant(i)
		if !ok || got != w {
			t.Fatalf("sliceConstant(%d) = %d, %v; want %d, true", i, got, ok, w)
		}
	}

	if _, ok := sliceConstant(maxInputSlices); ok {
		t.Fatalf("sliceConstant(%d) succeeded; the field only has %d constants", maxInputSlices, maxInputSlices)
	}
	if _, ok := sliceConstant(-1); ok {
		t.Fatal("sliceConstant(-1) succeeded")
	}

	// Distinct constants are what makes the recovery matrix solvable, and
	// order 65535 is the constraint the spec states.
	seen := make(map[uint16]int, maxInputSlices)
	for i := 0; i < maxInputSlices; i++ {
		c, ok := sliceConstant(i)
		if !ok {
			t.Fatalf("sliceConstant(%d) failed", i)
		}
		if c == 0 {
			t.Fatalf("sliceConstant(%d) is zero", i)
		}
		if prev, dup := seen[c]; dup {
			t.Fatalf("sliceConstant(%d) duplicates sliceConstant(%d) (%d)", i, prev, c)
		}
		seen[c] = i
	}
	// A power of two has order 65535 exactly when its exponent is coprime to
	// 65535, so raising it to any maximal proper divisor must not give 1.
	for _, p := range []uint32{3, 5, 17, 257} {
		d := uint32(gfLimit) / p
		for i := 0; i < maxInputSlices; i += 97 {
			c, _ := sliceConstant(i)
			if gfPow(c, d) == 1 {
				t.Fatalf("sliceConstant(%d) = %d has order dividing %d, not %d", i, c, d, gfLimit)
			}
		}
	}
}

func TestMulAddSliceMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 500; trial++ {
		n := 2 * (1 + rng.Intn(64))
		src := make([]byte, n)
		dst := make([]byte, n)
		rng.Read(src)
		rng.Read(dst)
		factor := uint16(rng.Intn(gfOrder))

		want := make([]byte, n)
		copy(want, dst)
		for i := 0; i+1 < n; i += 2 {
			v := uint16(src[i]) | uint16(src[i+1])<<8
			p := gfMul(v, factor)
			want[i] ^= byte(p)
			want[i+1] ^= byte(p >> 8)
		}

		got := make([]byte, n)
		copy(got, dst)
		mulAddSlice(got, src, factor)

		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("factor %d: byte %d = %02x, want %02x", factor, i, got[i], want[i])
			}
		}
	}
}

func TestInvertMatrixRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(4))

	// The shape a real repair builds: constant_j ^ exponent_r, for the first
	// n input constants and a set of exponents. Both the contiguous exponents
	// par2cmdline normally emits and a scattered subset are exercised, since
	// a set assembled from whichever volumes arrived gives us the latter.
	for _, exps := range [][]uint32{
		{0, 1, 2, 3, 4, 5},
		{0, 3, 7, 11, 19, 40},
		{5, 6, 7},
		{0},
	} {
		n := len(exps)
		m := make([]uint16, n*n)
		for r := 0; r < n; r++ {
			for j := 0; j < n; j++ {
				c, _ := sliceConstant(j * 3)
				m[r*n+j] = gfPow(c, exps[r])
			}
		}
		inv, err := invertMatrix(m, n)
		if err != nil {
			t.Fatalf("invertMatrix(%v): %v", exps, err)
		}
		assertIdentity(t, m, inv, n)
	}

	// Random matrices too: most are invertible, and the ones that are not must
	// be reported rather than mis-solved.
	inverted := 0
	for trial := 0; trial < 200; trial++ {
		n := 1 + rng.Intn(8)
		m := make([]uint16, n*n)
		for i := range m {
			m[i] = uint16(rng.Intn(gfOrder))
		}
		inv, err := invertMatrix(m, n)
		if err != nil {
			continue
		}
		assertIdentity(t, m, inv, n)
		inverted++
	}
	if inverted == 0 {
		t.Fatal("no random matrix inverted; the test is not exercising anything")
	}
}

func TestInvertMatrixSingular(t *testing.T) {
	// Two identical rows: not invertible, and the caller must hear about it
	// rather than get a zero pivot's worth of garbage.
	m := []uint16{
		1, 2, 3,
		1, 2, 3,
		4, 5, 6,
	}
	if _, err := invertMatrix(m, 3); err == nil {
		t.Fatal("invertMatrix accepted a singular matrix")
	}
	if _, err := invertMatrix([]uint16{0}, 1); err == nil {
		t.Fatal("invertMatrix accepted the zero matrix")
	}
}

func TestInvertMatrixDoesNotMutateInput(t *testing.T) {
	m := []uint16{1, 2, 3, 4}
	orig := []uint16{1, 2, 3, 4}
	if _, err := invertMatrix(m, 2); err != nil {
		t.Fatalf("invertMatrix: %v", err)
	}
	for i := range m {
		if m[i] != orig[i] {
			t.Fatalf("invertMatrix mutated its input at %d", i)
		}
	}
}

func assertIdentity(t *testing.T, m, inv []uint16, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			var acc uint16
			for k := 0; k < n; k++ {
				acc ^= gfMul(m[i*n+k], inv[k*n+j])
			}
			want := uint16(0)
			if i == j {
				want = 1
			}
			if acc != want {
				t.Fatalf("(m * inv)[%d][%d] = %d, want %d", i, j, acc, want)
			}
		}
	}
}
