// Package orca wraps the Orca CLI.
package orca

// Agent is one row of `orca agent list --json`.
type Agent struct {
	SessionID     string `json:"orcaSessionId"`
	Kind          string `json:"kind"`
	Agent         string `json:"agent"`
	State         string `json:"state"`
	Title         string `json:"title"`
	Workspace     string `json:"workspace"`
	Repo          string `json:"repo"`
	PaneKey       string `json:"paneKey"`
	LastAssistant string `json:"lastAssistantMessage"`
}
