package mooncake

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
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

// hashBlockSha256Hex replicates Python:
//
// def _get_hash_str(token_ids, prior_hash=None):
//
//	hasher = hashlib.sha256()
//	if prior_hash: hasher.update(bytes.fromhex(prior_hash))
//	for t in token_ids:
//	  if isinstance(t, tuple):
//	    for elem in t: hasher.update(elem.to_bytes(4,"little",signed=False))
//	  else:
//	    hasher.update(t.to_bytes(4,"little",signed=False))
//	return hasher.hexdigest()
func hashBlockSha256Hex(tokens []int64, priorHex string) (string, error) {
	h := sha256.New()

	if priorHex != "" {
		priorBytes, err := hex.DecodeString(priorHex)
		if err != nil {
			return "", fmt.Errorf("invalid prior_hash hex: %w", err)
		}
		if len(priorBytes) != sha256.Size {
			return "", fmt.Errorf("prior_hash must decode to %d bytes, got %d", sha256.Size, len(priorBytes))
		}
		_, _ = h.Write(priorBytes)
	}

	var buf [4]byte
	for _, t := range tokens {
		// to_bytes(4, signed=False) requires 0 <= t <= 2^32-1
		if t < 0 || t > int64(^uint32(0)) {
			return "", fmt.Errorf("token out of uint32 range: %d", t)
		}
		binary.LittleEndian.PutUint32(buf[:], uint32(t))
		_, _ = h.Write(buf[:])
	}

	return hex.EncodeToString(h.Sum(nil)), nil
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
