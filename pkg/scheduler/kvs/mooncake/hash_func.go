package mooncake

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"

	"github.com/fxamacker/cbor/v2"
)

// sha256CBOR serializes v with canonical CBOR (RFC 8949 canonical form)
// and returns SHA-256 digest bytes (32 bytes), matching:
//
//	cbor2.dumps(input, canonical=True)
//	hashlib.sha256(input_bytes).digest()
func sha256CBOR(v any) ([]byte, error) {
	// Canonical CBOR encoding (deterministic, sorted map keys, shortest forms, etc.)
	encMode, err := cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		return nil, err
	}
	b, err := encMode.Marshal(v)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return sum[:], nil
}

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
