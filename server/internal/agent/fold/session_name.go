package fold

import (
	"crypto/sha256"
	"encoding/hex"
)

// SessionFileName maps a session key to a file name. Session keys are slugs
// today, but they come from the client, so they are hashed rather than joined:
// a key such as "../../etc/passwd" must not escape the directory. Eight bytes
// of SHA-256 is plenty for the number of sessions one node holds.
func SessionFileName(sessionKey string) string {
	sum := sha256.Sum256([]byte(sessionKey))
	return hex.EncodeToString(sum[:8]) + ".jsonl"
}
