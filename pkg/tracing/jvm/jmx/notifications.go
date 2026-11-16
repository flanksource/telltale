package jmx

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flanksource/clicky/api"
)

// Subscriptions are the notification subscribers the agent buffers for. MBean is the
// object name or pattern subscribed to; nil Filters accept every notification type.
type Subscriptions []Subscription

type Subscription struct {
	ID       string   `json:"id"`
	MBean    string   `json:"mbean"`
	Filters  []string `json:"filters"`
	Capacity int      `json:"capacity"`
	Queued   int      `json:"queued"`
	Dropped  int64    `json:"dropped"`
	Active   bool     `json:"active"`
}

func DecodeSubscriptions(raw json.RawMessage) (Subscriptions, error) {
	var subscriptions Subscriptions
	if err := json.Unmarshal(raw, &subscriptions); err != nil {
		return nil, fmt.Errorf("decode %s result: %w", NotificationsListOp, err)
	}
	if subscriptions == nil {
		return nil, fmt.Errorf("%s returned no subscription list: %s", NotificationsListOp, raw)
	}
	for _, subscription := range subscriptions {
		if subscription.ID == "" || subscription.MBean == "" {
			return nil, fmt.Errorf("%s returned a subscription without an id or MBean: %s", NotificationsListOp, raw)
		}
	}
	return subscriptions, nil
}

func (Subscription) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("id").Label("Subscription").Build(),
		api.Column("mbean").Label("MBean").MaxWidth(80).Build(),
		api.Column("filters").Label("Types").MaxWidth(60).Build(),
		api.Column("queued").Label("Queued").Build(),
		api.Column("dropped").Label("Dropped").Build(),
		api.Column("capacity").Label("Capacity").Build(),
		api.Column("state").Label("State").Build(),
	}
}

func (s Subscription) Row() map[string]any {
	filters := "all"
	if s.Filters != nil {
		filters = strings.Join(s.Filters, ", ")
	}
	dropped := api.Text{}.Append(api.HumanNumber(s.Dropped).String())
	if s.Dropped > 0 {
		dropped = api.Text{}.Append(api.HumanNumber(s.Dropped).String(), "text-yellow-600")
	}
	state := api.Text{}.Append("frozen", "text-muted")
	if s.Active {
		state = api.Text{}.Append("active", "text-green-600")
	}
	return map[string]any{
		"id": s.ID, "mbean": s.MBean, "filters": filters, "queued": api.HumanNumber(int64(s.Queued)),
		"dropped": dropped, "capacity": api.HumanNumber(int64(s.Capacity)), "state": state,
	}
}

func (s Subscriptions) Pretty() api.Text {
	if len(s) == 0 {
		return api.Text{}.Append("no notification subscriptions", "text-muted")
	}
	var queued, dropped int64
	for _, subscription := range s {
		queued += int64(subscription.Queued)
		dropped += subscription.Dropped
	}
	return api.Text{}.Append("Notification subscriptions", "font-bold").
		Append(fmt.Sprintf(" %d · %s queued · %s dropped", len(s), api.HumanNumber(queued).String(), api.HumanNumber(dropped).String()), "text-muted").
		NewLine().Add(api.NewTableFrom(s))
}
