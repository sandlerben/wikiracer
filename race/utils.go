package race

import "sync"

type concurrentMap struct {
	sync.RWMutex
	m map[string]string
}

// put(k,v) maps k to v in the map
func (c *concurrentMap) put(k string, v string) {
	c.Lock()
	c.m[k] = v
	c.Unlock()
}

// get(k) returns the value of k in the map
func (c *concurrentMap) get(k string) (string, bool) {
	c.RLock()
	v, ok := c.m[k]
	c.RUnlock()
	return v, ok
}
