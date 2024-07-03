package feeds

type UpdateType int

const (
	NewRelease UpdateType = iota
	Retag
	DescriptionChange
)

func (t UpdateType) String() string {
	switch t {
	case NewRelease:
		return " tagged: "
	case Retag:
		return " re-tagged: "
	case DescriptionChange:
		return " description changed: "
	default:
		return " (unhandled update type): "
	}
}

type Update struct {
	Type   UpdateType
	Repo   string
	Filter string

	Title   string
	Content string
	Link    string
}
