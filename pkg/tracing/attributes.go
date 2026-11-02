package tracing

// MaskAttribute applies the capture redaction policy to an attribute and its nested values.
func MaskAttribute(name string, value any) any {
	return secretMasker{}.entry(name, value)
}
