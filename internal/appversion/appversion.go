package appversion

import "sync/atomic"

var value atomic.Value // stores string

// Set records the current application version for chassis-wide consumers.
func Set(v string) {
	value.Store(v)
}

// Get returns the current application version, if one has been set.
func Get() string {
	v, _ := value.Load().(string)
	return v
}
