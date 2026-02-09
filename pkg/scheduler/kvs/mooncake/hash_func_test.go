package mooncake

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

type tuple2 struct {
	A []int64
	B uint64
}

func mkSeq(n int) []int64 {
	out := make([]int64, n)
	for i := 0; i < n; i++ {
		v := int64(i + 1)
		if i%3 == 1 {
			v = -v
		}
		out[i] = v
	}
	return out
}

func pyTupleExprFromSeq(seq []int64, u uint64) string {
	var b strings.Builder
	b.WriteString("(")
	b.WriteString(fmt.Sprintf("%d", u))
	b.WriteString(", ")
	b.WriteString("[")
	for i, v := range seq {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(fmt.Sprintf("%d", v))
	}
	b.WriteString("]")
	// 3) null/None last
	b.WriteString(", None)")
	return b.String()
}

func TestSha256CBOR_MatchesPython_TupleListLenAndPrefixHash(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found in PATH")
	}
	lengths := []int{16, 32, 64}
	prefixes := []uint64{
		0x0000000000000000,
		0x0123456789abcdef,
		0x1032547698badcfe,
		0xdeadbeefcafebabe,
		0x7fffffffffffffff,
		0x8000000000000000,
		0xffffffffffffffff,
		0x9e3779b97f4a7c15,
	}
	for _, n := range lengths {
		seq := mkSeq(n)
		t.Run(fmt.Sprintf("len=%d_fixedPrefix", n), func(t *testing.T) {
			p := prefixes[3]
			pyExpr := pyTupleExprFromSeq(seq, p)
			want, err := pythonSha256CBOR(pyExpr)
			if err != nil {
				t.Fatalf("pythonSha256CBOR error: %v", err)
			}
			got, err := sha256CBOR([]any{p, seq, nil})
			get := hex.EncodeToString(got)
			if err != nil {
				t.Fatalf("Sha256CBOR error: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("hash mismatch:\n  got : %x\n  want: %x\n  pyExpr: %s", get, want, pyExpr)
			}
		})
		t.Run(fmt.Sprintf("len=%d_varyPrefix", n), func(t *testing.T) {
			for _, p := range prefixes {
				t.Run(fmt.Sprintf("prefix=%016x", p), func(t *testing.T) {
					pyExpr := pyTupleExprFromSeq(seq, p)
					want, err := pythonSha256CBOR(pyExpr)
					if err != nil {
						t.Fatalf("pythonSha256CBOR error: %v", err)
					}
					got, err := sha256CBOR([]any{p, seq, nil})
					get := hex.EncodeToString(got)
					if err != nil {
						t.Fatalf("Sha256CBOR error: %v", err)
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("hash mismatch:\n  got : %x\n  want: %x\n  pyExpr: %s", get, want, pyExpr)
					}
				})
			}
		})
	}
}

func TestGetHashStr_MatchesPython_Regular(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found in PATH")
	}

	tests := []struct {
		name  string
		toks  []int64
		prior string
	}{
		{
			name:  "empty_no_prior",
			toks:  []int64{},
			prior: "",
		},
		{
			name:  "small_ints",
			toks:  []int64{1, 2, 3, 255, 256, 65535, 65536},
			prior: "",
		},
		{
			name:  "max_u32",
			toks:  []int64{0, 4294967295},
			prior: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, err := pythonHashBlockSha256Hex(tt.toks, tt.prior)
			if err != nil {
				t.Fatalf("pythonHashBlockSha256Hex error: %v", err)
			}
			got, err := hashBlockSha256Hex(tt.toks, tt.prior)
			if err != nil {
				t.Fatalf("hashBlockSha256Hex error: %v", err)
			}
			if got != want {
				t.Fatalf("hash mismatch:\n  got : %s\n  want: %s", got, want)
			}
		})
	}
}

func TestGetHashStr_MatchesPython_Chaining(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found in PATH")
	}

	toks1 := []int64{1, 2, 3, 4, 5}
	toks2 := []int64{100, 200, 300}

	py1, err := pythonHashBlockSha256Hex(toks1, "")
	if err != nil {
		t.Fatalf("pythonHashBlockSha256Hex(1) error: %v", err)
	}
	go1, err := hashBlockSha256Hex(toks1, "")
	if err != nil {
		t.Fatalf("hashBlockSha256Hex(1) error: %v", err)
	}
	if go1 != py1 {
		t.Fatalf("hash1 mismatch:\n  got : %s\n  want: %s", go1, py1)
	}

	py2, err := pythonHashBlockSha256Hex(toks2, py1)
	if err != nil {
		t.Fatalf("pythonHashBlockSha256Hex(2) error: %v", err)
	}
	go2, err := hashBlockSha256Hex(toks2, go1)
	if err != nil {
		t.Fatalf("hashBlockSha256Hex(2) error: %v", err)
	}
	if go2 != py2 {
		t.Fatalf("hash2 mismatch:\n  got : %s\n  want: %s", go2, py2)
	}
}

func TestGetHashStr_RangeChecks(t *testing.T) {
	// Negative => Python would raise OverflowError; Go should return error.
	if _, err := hashBlockSha256Hex([]int64{-1}, ""); err == nil {
		t.Fatalf("expected error for negative token")
	}

	// > uint32 max
	if _, err := hashBlockSha256Hex([]int64{4294967296}, ""); err == nil {
		t.Fatalf("expected error for >uint32 token")
	}

	// prior hash malformed
	if _, err := hashBlockSha256Hex([]int64{1}, "zz"); err == nil {
		t.Fatalf("expected error for invalid prior hash hex")
	}
}

func TestGetHashStr_ManyRandomLikeCases_MatchesPython(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found in PATH")
	}

	for i := 0; i < 50; i++ {
		toks := make([]int64, 0, 20)
		for j := 0; j < 20; j++ {
			v := int64((i+1)*(j+3)) * 12345
			v = v % 100000 // keep in uint32 range and non-negative
			toks = append(toks, v)
		}

		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			want, err := pythonHashBlockSha256Hex(toks, "")
			if err != nil {
				t.Fatalf("pythonHashBlockSha256Hex error: %v", err)
			}
			got, err := hashBlockSha256Hex(toks, "")
			if err != nil {
				t.Fatalf("hashBlockSha256Hex error: %v", err)
			}
			if got != want {
				t.Fatalf("hash mismatch:\n  got : %s\n  want: %s", got, want)
			}
		})
	}
}
