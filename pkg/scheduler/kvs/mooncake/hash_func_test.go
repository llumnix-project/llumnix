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
