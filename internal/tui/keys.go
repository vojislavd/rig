package tui

// Key is one keypress.
type Key int

const (
	KeyNone Key = iota
	KeyUp
	KeyDown
	KeyQuit
	KeyRefresh
	KeyOpen
	KeyQueue
	KeySchedule
	KeyReverb
	KeyMail
	KeyS3
)

// parseKeys turns bytes read from the terminal into keys. Unknown input
// is ignored.
func parseKeys(b []byte) []Key {
	var keys []Key
	for i := 0; i < len(b); i++ {
		// Arrow keys: ESC [ A (up) / ESC [ B (down).
		if b[i] == 0x1b && i+2 < len(b) && b[i+1] == '[' {
			switch b[i+2] {
			case 'A':
				keys = append(keys, KeyUp)
			case 'B':
				keys = append(keys, KeyDown)
			}
			i += 2
			continue
		}
		switch b[i] {
		case 'k':
			keys = append(keys, KeyUp)
		case 'j':
			keys = append(keys, KeyDown)
		case 'q', 0x03: // 0x03 is Ctrl+C
			keys = append(keys, KeyQuit)
		case 'r':
			keys = append(keys, KeyRefresh)
		case 'o', '\r':
			keys = append(keys, KeyOpen)
		case '1':
			keys = append(keys, KeyQueue)
		case '2':
			keys = append(keys, KeySchedule)
		case '3':
			keys = append(keys, KeyReverb)
		case 'm':
			keys = append(keys, KeyMail)
		case 's':
			keys = append(keys, KeyS3)
		}
	}
	return keys
}
