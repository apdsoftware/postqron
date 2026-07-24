package contentassistant

// textDiff returns a deterministic, lossless diff. It preserves the longest
// shared rune prefix and suffix and represents the changed middle explicitly.
func textDiff(original, proposed string) []DiffSegment {
	left := []rune(original)
	right := []rune(proposed)

	prefix := 0
	for prefix < len(left) &&
		prefix < len(right) &&
		left[prefix] == right[prefix] {
		prefix++
	}

	suffix := 0
	for suffix < len(left)-prefix &&
		suffix < len(right)-prefix &&
		left[len(left)-1-suffix] == right[len(right)-1-suffix] {
		suffix++
	}

	segments := make([]DiffSegment, 0, 4)
	appendSegment := func(operation DiffOperation, runes []rune) {
		if len(runes) == 0 {
			return
		}
		segments = append(segments, DiffSegment{
			Operation: operation,
			Text:      string(runes),
		})
	}
	appendSegment(DiffEqual, left[:prefix])
	appendSegment(DiffDelete, left[prefix:len(left)-suffix])
	appendSegment(DiffInsert, right[prefix:len(right)-suffix])
	if suffix > 0 {
		appendSegment(DiffEqual, left[len(left)-suffix:])
	}
	return segments
}
