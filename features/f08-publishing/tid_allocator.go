package publishing

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

const maxSignedTID = uint64(1<<63 - 1)

func monotonicTIDFloor(
	namespace, idempotencyKey string,
	physicalMicroseconds int64,
) (uint64, error) {
	namespace = strings.TrimSpace(namespace)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if namespace == "" || len(namespace) > 1024 ||
		idempotencyKey == "" || len(idempotencyKey) > 256 ||
		physicalMicroseconds < 0 ||
		uint64(physicalMicroseconds) >= uint64(1)<<53 {
		return 0, fmt.Errorf("%w: invalid TID allocation", ErrInvalidArgument)
	}
	digest := sha256.Sum256([]byte(namespace))
	clockID := uint64(binary.BigEndian.Uint16(digest[:2]) & ((1 << 10) - 1))
	value := uint64(physicalMicroseconds)<<10 | clockID
	if value > maxSignedTID {
		return 0, fmt.Errorf("%w: invalid TID allocation", ErrInvalidArgument)
	}
	return value, nil
}
