// Package seed defines the JSON schema of data/seed/*.json files — port of
// the DocumentSeed/ProvisionSeed/DefinitionSeed interfaces in
// scripts/build-db.ts:23-51. Shared by cmd/build-db and cmd/ingest.
package seed

type DocumentSeed struct {
	ID          string           `json:"id"`
	Type        string           `json:"type"` // defaults to 'statute'
	Title       string           `json:"title"`
	TitleEn     string           `json:"title_en,omitempty"`
	ShortName   string           `json:"short_name,omitempty"`
	Status      string           `json:"status"` // defaults to 'in_force'
	IssuedDate  string           `json:"issued_date,omitempty"`
	InForceDate string           `json:"in_force_date,omitempty"`
	URL         string           `json:"url,omitempty"`
	Description string           `json:"description,omitempty"`
	Provisions  []ProvisionSeed  `json:"provisions,omitempty"`
	Definitions []DefinitionSeed `json:"definitions,omitempty"`
}

type ProvisionSeed struct {
	ProvisionRef string         `json:"provision_ref"`
	Chapter      string         `json:"chapter,omitempty"`
	Section      string         `json:"section"`
	Title        string         `json:"title,omitempty"`
	Content      string         `json:"content"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type DefinitionSeed struct {
	Term            string `json:"term"`
	Definition      string `json:"definition"`
	SourceProvision string `json:"source_provision,omitempty"`
}
