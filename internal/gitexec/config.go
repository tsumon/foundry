// Package gitexec isolates fixed, independently installed Git from user config.
package gitexec

type Config struct {
	Path        string
	Environment []string
	Hooks       string
	Root        string
	cleanup     func()
}

func (c Config) Close() {
	if c.cleanup != nil {
		c.cleanup()
	}
}
