package codex

import "fmt"

// The explicit config override takes precedence over both copied config.toml
// and inherited CODEX_SQLITE_HOME. Legacy homes retain their original settings.
func isolatedStateArgs(c Config, args []string) []string {
	if !c.IsolateState {
		return args
	}
	return append([]string{"-c", fmt.Sprintf("sqlite_home=%q", c.Home)}, args...)
}
