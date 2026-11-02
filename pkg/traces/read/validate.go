package read

import "context"

// ValidateSelection checks profile parameters before a streaming response starts.
func ValidateSelection(ctx context.Context, refs []Ref, q Query) error {
	if err := q.Validate(); err != nil {
		return err
	}
	for _, ref := range refs {
		_, profile, err := ref.profile(ctx, "")
		if err != nil {
			return err
		}
		if _, err := q.params(ref, profile); err != nil {
			return err
		}
	}
	return nil
}
