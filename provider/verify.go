package provider

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
)

var (
	// ErrInvalidSource means a provider source address string couldn't be
	// parsed (see ParseSource).
	ErrInvalidSource = errors.New("invalid provider source address")

	// ErrPlatformNotFound means the registry has no release of this
	// provider@version for the requested os/arch.
	ErrPlatformNotFound = errors.New("provider release not found for this platform")

	// ErrChecksumNotFound means the requested platform's filename isn't
	// listed in the release's SHA256SUMS file at all.
	ErrChecksumNotFound = errors.New("provider archive not listed in SHA256SUMS")

	// ErrChecksumMismatch means the downloaded provider archive's SHA-256
	// doesn't match the (signature-verified) SHA256SUMS entry for it —
	// a corrupted or tampered download.
	ErrChecksumMismatch = errors.New("provider archive checksum mismatch")

	// ErrBadSignature means SHA256SUMS's detached signature didn't verify
	// against any of the registry-served signing keys.
	ErrBadSignature = errors.New("provider SHA256SUMS signature verification failed")

	// ErrNoSigningKeys means the registry response carried no signing keys
	// to verify against at all.
	ErrNoSigningKeys = errors.New("registry response has no signing keys")
)

// verifyChecksumsSignature confirms shasumsContent is signed by one of the
// registry-served signing keys, per sigBytes (a binary/non-armored
// detached OpenPGP signature — confirmed live: registry.opentofu.org's
// *_SHA256SUMS.sig files are raw binary, not ASCII-armored).
func verifyChecksumsSignature(shasumsContent, sigBytes []byte, keys []registryGPGPublicKey) error {
	if len(keys) == 0 {
		return ErrNoSigningKeys
	}

	var keyring openpgp.EntityList
	for _, k := range keys {
		entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(k.ASCIIArmor))
		if err != nil {
			continue // an unparseable key just doesn't participate; other keys might still verify
		}
		keyring = append(keyring, entities...)
	}
	if len(keyring) == 0 {
		return fmt.Errorf("%w: none of the %d registry-served keys could be parsed", ErrNoSigningKeys, len(keys))
	}

	if _, err := openpgp.CheckDetachedSignature(keyring, bytes.NewReader(shasumsContent), bytes.NewReader(sigBytes), nil); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	return nil
}

// expectedSHA256 finds filename's SHA-256 digest in a SHA256SUMS file
// (standard `sha256sum` output: "<hex digest>  <filename>" per line).
func expectedSHA256(shasumsContent []byte, filename string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(shasumsContent))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[1] == filename {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read SHA256SUMS: %w", err)
	}
	return "", fmt.Errorf("%w: %s", ErrChecksumNotFound, filename)
}

// sha256Hex returns the lowercase hex-encoded SHA-256 digest of b.
func sha256HexOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
