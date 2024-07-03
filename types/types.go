package types

import "strings"

type NotificationMessage struct {
	ChatID  int64
	Message string
}

// TODO: Create a proper text to markdown converter
var MdReplacer = strings.NewReplacer(
	".", "\\.",
	"-", "\\-",
	"#", "\\#",
	",", "\\,",
	"(", "\\(",
	")", "\\)",
	"[", "\\[",
	"]", "\\]",
	"{", "\\{",
	"}", "\\}",
	"+", "\\+",
)
