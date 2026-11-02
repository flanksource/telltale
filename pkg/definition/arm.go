package definition

import (
	"errors"
	"strings"

	"github.com/flanksource/telltale"
)

// Arm is options with request's captures armed (telltale.Definitions): the
// stored definition it names, recorded on the run, else its ad-hoc traces
// compiled as the inline definition Inline makes of them, recorded on none.
// Refusals are a *telltale.ValidationError at the request's pointers.
func (s *Service) Arm(request telltale.StartRequest, options telltale.StartOptions) (telltale.StartOptions, error) {
	if request.Definition != "" {
		stored, err := s.store.Get(request.Definition)
		if err != nil {
			return options, refused("/definition", err)
		}
		applied, err := Apply(stored.Definition, options)
		if err != nil {
			return options, requestRefusal(err, func(pointer string) (string, string) { return "/definition", pointer })
		}
		return applied, nil
	}
	captures, fields := request.Captures()
	def, pointers, placed := Inline(captures)
	fields = append(fields, placed...)
	applied, err := Apply(def, options)
	if err != nil {
		refusal := requestRefusal(err, func(pointer string) (string, string) { return capturePointer(pointers, pointer), "" })
		fields = append(fields, refusal.Fields...)
	}
	if len(fields) > 0 {
		return options, &telltale.ValidationError{Fields: fields}
	}
	applied.Definition = nil
	return applied, nil
}

// capturePointer is the request pointer of a pointer into an inline
// definition: /traces/<kind> and what follows the entry's own pointer.
func capturePointer(pointers map[string]string, pointer string) string {
	for entry, capture := range pointers {
		if rest, ok := strings.CutPrefix(pointer, entry); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
			return capture + rest
		}
	}
	return "/traces"
}

// requestRefusal is err at the request's pointers: each field of a refused
// definition at the request pointer at maps it to, its definition pointer, if
// any, kept in its message; any other error at the request's root.
func requestRefusal(err error, at func(pointer string) (string, string)) *telltale.ValidationError {
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		pointer, _ := at("")
		return refused(pointer, err)
	}
	fields := make([]telltale.FieldError, 0, len(invalid.Fields))
	for _, field := range invalid.Fields {
		pointer, inside := at(field.InstancePath)
		message := field.Message
		if inside != "" {
			message = inside + ": " + message
		}
		fields = append(fields, telltale.FieldError{InstancePath: pointer, Message: message})
	}
	return &telltale.ValidationError{Fields: fields}
}

func refused(pointer string, err error) *telltale.ValidationError {
	return &telltale.ValidationError{Fields: []telltale.FieldError{{InstancePath: pointer, Message: err.Error()}}}
}
