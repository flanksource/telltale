package definition

import "github.com/flanksource/telltale"

// Counts are how many entries a definition holds per section.
type Counts struct {
	Proxy      int `json:"proxy"`
	Instrument int `json:"instrument"`
	Collect    int `json:"collect"`
}

// Summary is a definition as a list shows it.
type Summary struct {
	Name            string `json:"name"`
	Layer           string `json:"layer"`
	Path            string `json:"path"`
	ResourceVersion string `json:"resourceVersion"`
	Description     string `json:"description,omitempty"`
	Counts          Counts `json:"counts"`
	// Problem is why the file defines no usable definition.
	Problem string `json:"problem,omitempty"`
}

// EntryView is one entry of a definition as resolved: where it is, the kind
// it compiles to, its name and the connection it reads.
type EntryView struct {
	Pointer     string `json:"pointer"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	DefaultName string `json:"defaultName"`
	// Connection is the one the entry names, else the default it takes.
	Connection string `json:"connection,omitempty"`
}

// Detail is one stored definition: its file and its resolved entries.
type Detail struct {
	Name            string           `json:"name"`
	Layer           string           `json:"layer"`
	Path            string           `json:"path"`
	ResourceVersion string           `json:"resourceVersion"`
	Definition      *TraceDefinition `json:"definition"`
	YAML            string           `json:"yaml"`
	Entries         []EntryView      `json:"entries"`
}

// ListView is one typed list a definition can hold, as a form adds to it.
type ListView struct {
	Path        string `json:"path"`
	Section     string `json:"section"`
	Group       string `json:"group,omitempty"`
	Type        string `json:"type"`
	Kind        string `json:"kind"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// SchemaView is what an editor renders a definition with.
type SchemaView struct {
	Schema map[string]any `json:"schema"`
	Lists  []ListView     `json:"lists"`
	Layers []Layer        `json:"layers"`
}

// Validation is a definition's static errors and its entries as resolved,
// with as much resolved as the errors allow.
type Validation struct {
	Errors  []telltale.FieldError `json:"errors"`
	Entries []EntryView           `json:"entries"`
}

// Summaries are stored as a list shows them.
func Summaries(stored []Stored) []Summary {
	summaries := make([]Summary, 0, len(stored))
	for _, one := range stored {
		summary := Summary{Name: one.Name, Layer: one.Layer, Path: one.Path, ResourceVersion: one.ResourceVersion, Problem: one.Problem}
		if one.Definition != nil {
			summary.Description = one.Definition.Spec.Description
			for _, at := range Walk(&one.Definition.Spec) {
				switch at.Section {
				case SectionProxy:
					summary.Counts.Proxy++
				case SectionInstrument:
					summary.Counts.Instrument++
				case SectionCollect:
					summary.Counts.Collect++
				}
			}
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

// Schema is the CRD's schema, every typed list and the store's layers.
func (s *Service) Schema() (SchemaView, error) {
	schema, err := Schema()
	if err != nil {
		return SchemaView{}, err
	}
	lists := Lists()
	views := make([]ListView, 0, len(lists))
	for _, at := range lists {
		views = append(views, ListView{
			Path: at.Path(), Section: at.Section, Group: at.Group, Type: at.Type, Kind: at.Kind, Title: at.Title, Description: at.Description,
		})
	}
	return SchemaView{Schema: schema, Lists: views, Layers: s.store.LayerDirs()}, nil
}

// Describe is stored with its entries resolved.
func Describe(stored Stored, options Options) Detail {
	return Detail{
		Name: stored.Name, Layer: stored.Layer, Path: stored.Path, ResourceVersion: stored.ResourceVersion,
		Definition: stored.Definition, YAML: string(stored.Data), Entries: Check(stored.Definition, options).Entries,
	}
}

// Check is def's static validation (Validate) and its entries as resolved.
func Check(def *TraceDefinition, options Options) Validation {
	_, refusals := compile(def, options)
	validation := Validation{Errors: refusals.Fields, Entries: []EntryView{}}
	if validation.Errors == nil {
		validation.Errors = []telltale.FieldError{}
	}
	resolved, _ := resolve(def, options) // its refusals are compile's, already in Errors
	if resolved == nil {
		return validation
	}
	for _, at := range resolved.Entries {
		view := EntryView{Pointer: at.Pointer(), Path: at.Path(), Kind: at.Kind, Name: at.Name, DefaultName: at.DefaultName(def.Name)}
		if carrier, ok := at.Entry.(connectionEntry); ok {
			if slot := carrier.connection(at); slot.field != nil {
				view.Connection = *slot.field
			}
		}
		validation.Entries = append(validation.Entries, view)
	}
	return validation
}
