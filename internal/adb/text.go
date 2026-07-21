package adb

// IsASCII reports whether s contains only ASCII characters (0x00-0x7F).
func IsASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7F {
			return false
		}
	}
	return true
}
