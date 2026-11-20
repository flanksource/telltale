package tracing

import (
	"errors"
	"fmt"
	"slices"

	"github.com/flanksource/telltale/pkg/tracing/target"
)

// ErrPresetSeam identifies a start that names another seam than the one its
// preset arms.
var ErrPresetSeam = errors.New("the kind's preset fixes the target seam")

// PresetOptions describe a seam preset of a kind: the base kind armed on one
// fixed seam, under its own kind name and title.
type PresetOptions[P any] struct {
	// Title names the preset in a start form and in help.
	Title string
	// Seam is the one seam the preset arms; a start may name it, never another.
	Seam target.Seam
	// Defaults adjusts a copy of the base kind's defaults for the preset.
	Defaults func(*P)
}

// Preset is a new plugin of h's capture armed only on options.Seam, starting
// from h's defaults adjusted by options.Defaults. Registered under its own
// name, its records are h's record type, stored and read under that name. A
// preset h could not serve is refused when it is registered.
func (h *Handler[P, R]) Preset(options PresetOptions[P]) *Handler[P, R] {
	preset := *h
	preset.title, preset.presetSeam = options.Title, options.Seam
	preset.seams, preset.defaultSeam = []target.Seam{options.Seam}, options.Seam
	if preset.invalid = h.presetError(options); preset.invalid != nil {
		return &preset
	}
	defaults, err := h.params()
	if err != nil {
		preset.invalid = err
		return &preset
	}
	if options.Defaults != nil {
		options.Defaults(&defaults)
	}
	preset.defaults = &defaults
	if seam, err := seamOf(defaults); err != nil {
		preset.invalid = fmt.Errorf("preset defaults: %w", err)
	} else if seam != "" && seam != options.Seam {
		preset.invalid = fmt.Errorf("preset defaults name seam %q, not the preset's seam %q", seam, options.Seam)
	}
	return &preset
}

func (h *Handler[P, R]) presetError(options PresetOptions[P]) error {
	switch {
	case h.invalid != nil:
		return h.invalid
	case options.Title == "":
		return errors.New("a preset needs a title")
	case options.Seam == "":
		return errors.New("a preset needs a seam")
	case !slices.Contains(h.seams, options.Seam):
		return fmt.Errorf("preset seam %q is not among the base kind's seams %v", options.Seam, h.seams)
	case len(h.tables) > 0:
		return errors.New("a kind with result tables cannot have presets")
	}
	return nil
}

// presetSeamError refuses a start naming another seam than the preset's.
func (h *Handler[P, R]) presetSeamError(selected target.Seam) error {
	if h.presetSeam == "" || selected == "" || selected == h.presetSeam {
		return nil
	}
	return fmt.Errorf("%w: kind %q arms seam %q, not %q", ErrPresetSeam, h.kind, h.presetSeam, selected)
}

// seamOf is the seam params' target names, if any.
func seamOf(params any) (target.Seam, error) {
	var selection struct {
		Target target.Target `json:"target"`
	}
	if err := roundTrip(params, &selection); err != nil {
		return "", err
	}
	return selection.Target.Seam, nil
}
