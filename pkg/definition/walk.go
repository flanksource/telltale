package definition

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/flanksource/telltale/pkg/tracing/target"
)

// Section names.
const (
	SectionProxy      = "proxy"
	SectionInstrument = "instrument"
	SectionCollect    = "collect"
)

// Located is one entry of a definition where the walker found it: its path,
// the kind its list compiles to, the seam it is aimed at and its resolved name.
type Located struct {
	// Section, Group and Type are the path's segments; Group is empty under
	// proxy, whose lists sit directly in the section.
	Section, Group, Type string
	// Index is the entry's 0-based position in its list of Count entries.
	Index, Count int
	// Kind is the trace kind id the list compiles to.
	Kind string
	Seam target.Seam
	// Backend fixes a logs list's cloud backend; Provider a query list's.
	Backend, Provider string
	// Preset is a list of a preset group, which an ad-hoc capture another
	// list also takes does not land in.
	Preset bool
	// Title and Description are the list's, as its form shows them.
	Title, Description string
	// Name is the entry's name, or its default once Resolve filled it.
	Name  string
	Entry Entry
}

// Path is the list's dotted path, such as collect.prometheus.scrape.
func (l Located) Path() string {
	parts := []string{l.Section}
	if l.Group != "" {
		parts = append(parts, l.Group)
	}
	return strings.Join(append(parts, l.Type), ".")
}

// Pointer is the entry's JSON pointer in the resource, as a FieldError names it.
func (l Located) Pointer() string {
	parts := []string{"", "spec", l.Section}
	if l.Group != "" {
		parts = append(parts, l.Group)
	}
	return strings.Join(append(parts, l.Type, strconv.Itoa(l.Index)), "/")
}

// DefaultName is <definition>-<section>[-<group>]-<type>, plus -<n> (its
// 1-based position) when its list holds more than one entry.
func (l Located) DefaultName(definition string) string {
	parts := []string{definition, l.Section}
	if l.Group != "" {
		parts = append(parts, l.Group)
	}
	parts = append(parts, l.Type)
	if l.Count > 1 {
		parts = append(parts, strconv.Itoa(l.Index+1))
	}
	return strings.Join(parts, "-")
}

// listTag is a typed list's telltale tag, or a group's (seam alone).
type listTag struct {
	kind, seam, backend, provider string
	preset                        bool
}

func parseTag(field reflect.StructField) listTag {
	var tag listTag
	for part := range strings.SplitSeq(field.Tag.Get("telltale"), ",") {
		key, value, _ := strings.Cut(part, "=")
		switch key {
		case "kind":
			tag.kind = value
		case "seam":
			tag.seam = value
		case "backend":
			tag.backend = value
		case "provider":
			tag.provider = value
		case "preset":
			tag.preset = true
		}
	}
	return tag
}

// clickyTag is the value of key in field's clicky tag, such as its title.
func clickyTag(field reflect.StructField, key string) string {
	for part := range strings.SplitSeq(field.Tag.Get("clicky"), ",") {
		if name, value, ok := strings.Cut(part, "="); ok && name == key {
			return value
		}
	}
	return ""
}

func jsonName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	return name
}

// Walk is every entry of spec, in field order, with its path, kind and seam;
// entries point into spec, so a change through one changes spec.
func Walk(spec *Spec) []Located {
	var found []Located
	for _, section := range []struct {
		name  string
		value reflect.Value
	}{
		{SectionProxy, reflect.ValueOf(&spec.Proxy).Elem()},
		{SectionInstrument, reflect.ValueOf(&spec.Instrument).Elem()},
		{SectionCollect, reflect.ValueOf(&spec.Collect).Elem()},
	} {
		found = append(found, walkGroup(section.name, "", listTag{}, section.value)...)
	}
	return found
}

// walkGroup walks value's lists, and the groups under a section.
func walkGroup(section, group string, groupTag listTag, value reflect.Value) []Located {
	var found []Located
	for i := range value.NumField() {
		field := value.Type().Field(i)
		tag := parseTag(field)
		if tag.kind == "" {
			if field.Type.Kind() == reflect.Struct && group == "" {
				found = append(found, walkGroup(section, jsonName(field), tag, value.Field(i))...)
			}
			continue
		}
		seam := tag.seam
		if seam == "" {
			seam = groupTag.seam
		}
		list := value.Field(i)
		for index := range list.Len() {
			entry, ok := list.Index(index).Addr().Interface().(Entry)
			if !ok {
				panic(fmt.Sprintf("trace definition: %s.%s.%s holds %s, which is no Entry", section, group, jsonName(field), field.Type.Elem()))
			}
			found = append(found, Located{
				Section: section, Group: group, Type: jsonName(field), Index: index, Count: list.Len(),
				Kind: tag.kind, Seam: target.Seam(seam), Backend: tag.backend, Provider: tag.provider, Preset: groupTag.preset,
				Title: clickyTag(field, "title"), Description: clickyTag(field, "desc"),
				Name: entry.EntryName(), Entry: entry,
			})
		}
	}
	return found
}

// Lists are every typed list a definition can hold, each as the Located of a
// zero entry: the paths, kinds and seams, whatever a definition holds.
func Lists() []Located {
	var spec Spec
	fill(reflect.ValueOf(&spec).Elem())
	found := Walk(&spec)
	for i := range found {
		found[i].Index, found[i].Count, found[i].Name = 0, 0, ""
	}
	return found
}

// fill gives every typed list under value one zero entry.
func fill(value reflect.Value) {
	for i := range value.NumField() {
		field := value.Type().Field(i)
		switch {
		case parseTag(field).kind != "":
			value.Field(i).Set(reflect.MakeSlice(field.Type, 1, 1))
		case field.Type.Kind() == reflect.Struct:
			fill(value.Field(i))
		}
	}
}
