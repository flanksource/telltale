package telltale

func (r *Run) outputRefs() *OutputRefs {
	if len(r.output) != 2 {
		return nil
	}
	return &OutputRefs{Stdout: StdoutFile, Stderr: StderrFile}
}
