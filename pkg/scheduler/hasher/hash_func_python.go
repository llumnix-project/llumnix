package hasher

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// pythonSha256CBOR runs Python to compute sha256(cbor2.dumps(obj, canonical=True)).digest().
// pyExpr should be a valid Python expression that evaluates to the object to be CBOR-serialized.
func pythonSha256CBOR(pyExpr string) ([]byte, error) {
	code := `
import cbor2, hashlib
obj = ` + pyExpr + `
b = cbor2.dumps(obj, canonical=True)
h = hashlib.sha256(b).digest()
print(h.hex())
`
	cmd := exec.Command("python3", "-c", code)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("python3 failed: %w; output:\n%s", err, string(out))
	}
	s := strings.TrimSpace(string(out))
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("failed to decode python hex %q: %w", s, err)
	}
	return raw, nil
}

// pythonHashBlockSha256Hex runs python3 to compute the same _get_hash_str (INT tokens only)
// and returns hexdigest string.
func pythonHashBlockSha256Hex(tokenIDs []int64, priorHashHex string) (string, error) {
	// Safer than building Python literals by hand: pass as JSON.
	tb, err := json.Marshal(tokenIDs)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(priorHashHex)
	if err != nil {
		return "", err
	}

	code := fmt.Sprintf(`
import hashlib, json

token_ids = json.loads(%q)
prior_hash = json.loads(%q)

def _get_hash_str(token_ids, prior_hash=None):
    hasher = hashlib.sha256()
    if prior_hash:
        hasher.update(bytes.fromhex(prior_hash))
    for t in token_ids:
        hasher.update(int(t).to_bytes(4, byteorder="little", signed=False))
    return hasher.hexdigest()

print(_get_hash_str(token_ids, prior_hash))
`, string(tb), string(pb))

	cmd := exec.Command("python3", "-c", code)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("python3 failed: %w; output:\n%s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

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
