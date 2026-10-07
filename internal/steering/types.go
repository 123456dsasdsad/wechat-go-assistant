package steering

type Receipt struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	TurnID string `json:"turn_id,omitempty"`
}
