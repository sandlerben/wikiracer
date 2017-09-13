// Package race encapsulates the logic for executing a wikirace.
package race

import (
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/pkg/errors"
)

// A Racer performs a wikipedia race.
type Racer interface {
	Run() ([]string, error)
}

type defaultRacer struct {
	startTitle    string
	endTitle      string
	prevMap       concurrentMap // mapping from childPage -> parentPage
	linksExplored concurrentMap // set of links which have passed through getLinks
	checkLinks    chan string   // links which may connect to endTitle
	getLinks      chan string   // parent links which should have children explored
	done          chan bool     // once closed, all goroutines exit
	closeOnce     sync.Once     // ensures that once is only closed once
	timeLimit     time.Duration // explored until this limit and then give up
	err           error         // err that should be passed back to requester
}

func newDefaultRacer(startTitle string, endTitle string, timeLimit time.Duration) *defaultRacer {
	r := new(defaultRacer)
	r.startTitle = startTitle
	r.endTitle = endTitle
	r.prevMap = concurrentMap{m: make(map[string]string)}
	r.linksExplored = concurrentMap{m: make(map[string]string)}
	r.checkLinks = make(chan string, checkLinksSize)
	r.getLinks = make(chan string, getLinksSize)
	r.done = make(chan bool, 1)
	r.timeLimit = timeLimit
	return r
}

// NewRacer returns a Racer which can run a race from start to end.
func NewRacer(startTitle string, endTitle string, timeLimit time.Duration) Racer {
	return newDefaultRacer(startTitle, endTitle, timeLimit)
}

// Run finds a path from start to end and returns it.
func (r *defaultRacer) Run() ([]string, error) {
	r.prevMap.put(r.startTitle, "")
	r.linksExplored.put(r.startTitle, "")
	r.getLinks <- r.startTitle
	r.checkLinks <- r.startTitle
	for i := 0; i < config.numCheckLinksRoutines; i++ {
		go r.checkLinksWorker()
	}
	for i := 0; i < config.numGetLinksRoutines; i++ {
		go r.getLinksWorker()
	}
	timer := time.NewTimer(r.timeLimit)
	go r.giveUpAfterTime(timer)
	_ = <-r.done

	if r.err != nil {
		return nil, errors.WithStack(r.err)
	}

	finalPath := make([]string, 0)

	currentNode := r.endTitle
	// if the racer ran out of time, the endTitle won't have been found
	if _, ok := r.prevMap.get(r.endTitle); !ok {
		finalPath = nil
	} else {
		for {
			// append to front
			finalPath = append([]string{currentNode}, finalPath...)
			nextNode, ok := r.prevMap.get(currentNode)

			if !ok || currentNode == r.startTitle {
				break
			}

			currentNode = nextNode
		}
	}
	log.Debugf("at end of Run(), checkLinks length is %d, getLinks length is %d", len(r.checkLinks), len(r.getLinks))
	return finalPath, nil
}
