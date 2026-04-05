package comb

func CombRange(startIdx, count uint64, alphabet string, maxLen int, fn func([]byte) bool) {
	if count == 0 || len(alphabet) == 0 || maxLen <= 0 {
		return
	}

	base := uint64(len(alphabet))
	alph := []byte(alphabet)
	buf := make([]byte, 0, maxLen)

	for n := startIdx; n < startIdx+count; n++ {
		word, ok := numberToBytes(n, alph, base, maxLen, buf)
		if !ok {
			return
		}
		if fn(word) {
			return
		}
	}
}

func numberToBytes(n uint64, alphabet []byte, base uint64, maxLen int, buf []byte) ([]byte, bool) {
	length := 1
	block := base
	for n >= block {
		n -= block
		length++
		if length > maxLen {
			return nil, false
		}
		block *= base
	}

	if cap(buf) < length {
		buf = make([]byte, length)
	}
	buf = buf[:length]

	for i := length - 1; i >= 0; i-- {
		buf[i] = alphabet[n%base]
		n /= base
	}
	return buf, true
}
