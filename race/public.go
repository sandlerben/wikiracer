// Package race encapsulates the logic for executing a wikirace.
package race

import (
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/pkg/errors"
)

var (
	forwardLinksChannelSize  = 10000000
	backwardLinksChannelSize = 10000000
)

// A Racer performs a wikipedia race.
type Racer interface {
	Run() ([]string, error)
}

type defaultRacer struct {
	startTitle string
	endTitle   string
	// mapping of pages to the page that linked to them (found from startTitle)
	pathFromStartMap concurrentMap
	// mapping of pages to the page they linked to (found from endTitle)
	pathFromEndMap concurrentMap
	// pages found exploring from startTitle which should be explored
	forwardLinks chan string
	// pages found exploring from endTitle which should be explored
	backwardLinks chan string
	// once closed, all goroutines exit
	done chan bool
	// ensures that `done` is only closed once
	closeOnce sync.Once
	// explored until this limit and then give up
	timeLimit time.Duration
	// err that should be passed back to requester
	err error
	// the page at which the connected component from startTitle meets the
	// conntected component from endTitle
	meetingPoint lockerString
}

func newDefaultRacer(startTitle string, endTitle string, timeLimit time.Duration) *defaultRacer {
	r := new(defaultRacer)
	r.startTitle = startTitle
	r.endTitle = endTitle
	r.pathFromStartMap = concurrentMap{m: make(map[string]string)}
	r.pathFromEndMap = concurrentMap{m: make(map[string]string)}
	r.forwardLinks = make(chan string, forwardLinksChannelSize)
	r.backwardLinks = make(chan string, backwardLinksChannelSize)
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
	r.pathFromStartMap.put(r.startTitle, "")
	r.pathFromEndMap.put(r.endTitle, "")
	r.forwardLinks <- r.startTitle
	r.backwardLinks <- r.endTitle

	for i := 0; i < config.numForwardLinksRoutines; i++ {
		go r.forwardLinksWorker()
	}
	for i := 0; i < config.numBackwardLinksRoutines; i++ {
		go r.backwardLinksWorker()
	}
	timer := time.NewTimer(r.timeLimit)
	go r.giveUpAfterTime(timer)
	_ = <-r.done

	log.Debugf("forwardLinks length is %d and backwardLinks length is %d", len(r.forwardLinks), len(r.backwardLinks))
	if r.err != nil {
		return nil, errors.WithStack(r.err)
	}

	// At this point, other goroutines may not have checked that done is closed
	// yet. Therefore, we lock the meetingPoint variable so that nobody else
	// overwrites it.
	r.meetingPoint.Lock()

	// time ran out
	if r.meetingPoint.s == "" {
		r.meetingPoint.Unlock()
		return nil, nil
	}

	// get the path from start, reverse it, and remove the last element
	// (which is the meeting point) since it will reappear in path from end
	pathFromStart := getPath(r.meetingPoint.s, &r.pathFromStartMap)
	reverse(pathFromStart)
	pathFromStart = pathFromStart[0 : len(pathFromStart)-1]

	pathFromEnd := getPath(r.meetingPoint.s, &r.pathFromEndMap)
	finalPath := append(pathFromStart, pathFromEnd...)

	r.meetingPoint.Unlock()
	return finalPath, nil
}
