//go:build !notui

package ask

// Card renders a one-line summary of the questions for the TUI tool card. It
// lives in a build-tagged file so the notui build keeps only the tool logic.
func Card(questions int) string {
	return "asked " + itoa(questions) + " question(s)"
}

// itoa is a tiny integer formatter that avoids importing strconv here.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
