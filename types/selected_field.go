package types

// SelectedField is the public representation of a field selection
// during a graphql query
type SelectedField struct {
	Name   string
	Fields []*SelectedField
}

type SelectedFields struct {
	Fields []*SelectedField
}
