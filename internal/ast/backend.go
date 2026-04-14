package ast

type BackendStatus struct {
	Preferred string `json:"preferred"`
	Active    string `json:"active"`
	Reason    string `json:"reason,omitempty"`
}

func (e *Engine) BackendStatus() BackendStatus {
	return currentBackendStatus()
}
