package tracing

import "encoding/json"

type recordPresenter[R any] interface {
	Present(R) RecordPresentation
}

func (h *Handler[P, R]) presentRecord(data []byte) (RecordPresentation, bool, error) {
	presenter, ok := h.handler.(recordPresenter[R])
	if !ok {
		return RecordPresentation{}, false, nil
	}
	var record R
	if err := json.Unmarshal(data, &record); err != nil {
		return RecordPresentation{}, true, err
	}
	return presenter.Present(record), true, nil
}
