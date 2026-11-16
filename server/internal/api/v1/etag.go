package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// taskETag is a weak validator over the task's state. Download URLs are
// re-signed on every request, so they are left out of the hash; instead,
// when the task has downloadable outputs, the current etagWindow slot is
// mixed in. A 304 is therefore only returned within the slot the client's
// copy was fetched in, while its URLs still have at least ttl-etagWindow left.
func taskETag(t *Task, now time.Time, window time.Duration) (string, error) {
	raw, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(raw)
	for i := range t.Jobs {
		if downloadable(&t.Jobs[i]) {
			h.Write([]byte("\x00slot:" + strconv.FormatInt(now.UnixNano()/int64(window), 10)))
			break
		}
	}
	return `W/"` + hex.EncodeToString(h.Sum(nil))[:32] + `"`, nil
}

// matchesETag implements the weak comparison of If-None-Match (RFC 9110 §13.1.2).
func matchesETag(ifNoneMatch, etag string) bool {
	opaque := strings.TrimPrefix(etag, "W/")
	for candidate := range strings.SplitSeq(ifNoneMatch, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == opaque {
			return true
		}
	}
	return false
}
