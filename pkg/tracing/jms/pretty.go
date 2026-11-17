package jms

import (
	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"time"
)

func (r Row) Pretty() api.Text {
	text := api.Text{}.Append("JMS "+r.Operation, "font-bold").Space().Append(r.Destination).Space().Append(r.Handler, "text-muted").Space().Add(clicky.Human(time.Duration(r.DurationMs * float64(time.Millisecond))))
	if r.Error != "" {
		text = text.Space().Append(r.Error, "text-red-500")
	}
	return text
}
func (r Row) PrettyFull() api.Textable {
	return r.Pretty().NewLine().Add(clicky.Map(map[string]any{"messageId": r.MessageID, "deliveryId": r.DeliveryID, "correlationId": r.CorrelationID, "outcome": r.Outcome, "body": r.Body}))
}
