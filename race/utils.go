package race

import "sync"

type lockerString struct {
	sync.Mutex
	s string
}

// put(k,v) maps k to v in the map
func (l *lockerString) set(k string) {
	l.Lock()
	l.s = k
	l.Unlock()
}

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

// getPath uses a mapping from nodes to other nodes to compute a path from start
func getPath(start string, pathMap *concurrentMap) []string {
	currentNode := start
	path := make([]string, 0)
	more := true

	for more && currentNode != "" {
		path = append(path, currentNode)

		currentNode, more = pathMap.get(currentNode)
	}

	return path
}

// reverse a string slice in place
func reverse(a []string) {
	for i := len(a)/2 - 1; i >= 0; i-- {
		opp := len(a) - 1 - i
		a[i], a[opp] = a[opp], a[i]
	}
}
