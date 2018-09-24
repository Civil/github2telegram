package feeds

type UpdateType int

const (
	NewRelease UpdateType = iota
	Retag
	DescriptionChange
)

type Update struct {
	Type   UpdateType
	Repo   string
	Filter string

	Title   string
	Content string
	Link    string
}