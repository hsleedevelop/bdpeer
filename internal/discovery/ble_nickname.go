package discovery

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const unknownBLENickname = "unknown"

func normalizeBLENickname(data []byte) string {
	if len(data) == 0 || !utf8.Valid(data) {
		return unknownBLENickname
	}
	nick := strings.TrimSpace(string(data))
	if nick == "" || len(nick) > 64 {
		return unknownBLENickname
	}
	for _, r := range nick {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			return unknownBLENickname
		}
	}
	return nick
}
