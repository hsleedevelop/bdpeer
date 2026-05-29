package ui

import "unicode/utf8"

func dropLastRune(s string) string {
	if s == "" {
		return ""
	}
	_, size := utf8.DecodeLastRuneInString(s)
	if size <= 0 {
		return ""
	}
	return s[:len(s)-size]
}

func inputLine(value string, width int) string {
	const prefix = "> "
	if width <= 0 {
		return ""
	}
	valueWidth := width - 2
	if valueWidth < 0 {
		return truncateForWidth(prefix, width)
	}
	return prefix + truncateForWidth(value, valueWidth)
}

func inputLineWithCursor(value string, width int) string {
	const cursor = "█"
	if width <= 0 {
		return ""
	}
	line := inputLine(value, width-1) + cursor
	return truncateForWidth(line, width)
}
