// TODO: document this package
package race

import (
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/buger/jsonparser"
	"github.com/pkg/errors"
)

type concurrentMap struct {
	sync.RWMutex
	m map[string]string
}

type Racer struct {
	startTitle    string
	endTitle      string
	prevMap       concurrentMap
	linksExplored concurrentMap
	wg            sync.WaitGroup
	checkLinks    chan string
	getLinks      chan string
	done          chan bool
	closeOnce     sync.Once
	err           error
}

// what do we do on success? how is that passed? and error?

var (
	checkLinksSize        = 10000000
	getLinksSize          = 10000000
	numCheckLinksRoutines = 2
	numGetLinksRoutines   = 2
)

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

func NewRacer(startTitle string, endTitle string) *Racer {
	r := new(Racer)
	r.startTitle = startTitle
	r.endTitle = endTitle
	// prev map which maps from page to the page that got you there
	r.prevMap = concurrentMap{m: make(map[string]string)}
	r.linksExplored = concurrentMap{m: make(map[string]string)}

	r.wg = sync.WaitGroup{}

	r.checkLinks = make(chan string, checkLinksSize)
	r.getLinks = make(chan string, getLinksSize)
	r.done = make(chan bool, 1)
	return r
}

func (r *Racer) handleGoroutineErr(err error) {
	log.Error("err occurred in goroutine")
	log.Errorf("%+v", err)
	r.err = err

	// sometimes multiple goroutines will try to close the done channel
	// defer func() {
	// 	if rec := recover(); r != nil {
	// 		log.Debug("Recovered in f", rec)
	// 	}
	// 	r.wg.Done()
	// }()
	r.closeOnce.Do(func() {
		close(r.done)
	}) // kill all goroutines
}

func (r *Racer) checkLinksWorker() {
	defer r.wg.Done()

	for {
		linksToCheck := make([]string, 0)
		keepGoing := true
		for keepGoing && len(linksToCheck) < 50 {
			select {
			case _ = <-r.done:
				return
			case link := <-r.checkLinks:
				// log.Debugf("got %s from checkLinks", link)
				linksToCheck = append(linksToCheck, link)
			default: // nothing to read on channel
				if len(linksToCheck) > 5 { // if we have at least 5, let's boogie
					keepGoing = false
				}
			}
		}
		// log.Debugf("linksToCheck is %v with length %d", linksToCheck, len(linksToCheck))

		u, err := url.Parse("https://en.wikipedia.org/w/api.php")
		if err != nil {
			r.handleGoroutineErr(errors.WithStack(err))
			return
		}
		q := u.Query()
		q.Set("action", "query")
		q.Set("format", "json")
		q.Set("prop", "links")
		q.Set("titles", strings.Join(linksToCheck, "|"))
		q.Set("redirects", "1")
		q.Set("formatversion", "2")
		q.Set("pllimit", "500")
		q.Set("pltitles", r.endTitle) // TODO: should we make sure this is real?
		u.RawQuery = q.Encode()

		resp, err := http.Get(u.String())
		if err != nil {
			r.handleGoroutineErr(errors.WithStack(err))
			return
		}
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			r.handleGoroutineErr(errors.WithStack(err))
			return
		}

		_, err = jsonparser.ArrayEach(bodyBytes, func(page []byte, dataType jsonparser.ValueType, offset int, err error) {
			linksData, dataType, _, err := jsonparser.Get(page, "links")
			if err != nil && dataType != jsonparser.NotExist {
				r.handleGoroutineErr(errors.WithStack(err))
				return
			}
			if len(linksData) > 0 { // found it!
				// figure out the page that got us there
				prevName, err := jsonparser.GetString(page, "title")
				if err != nil {
					r.handleGoroutineErr(errors.WithStack(err))
					return
				}
				log.Debugf("found %s via %s", string(linksData), prevName)

				r.prevMap.put(r.endTitle, prevName)
				r.closeOnce.Do(func() {
					close(r.done)
				}) // kill all goroutines
				return
			}
		}, "query", "pages")
		if err != nil {
			r.handleGoroutineErr(errors.Wrap(err, string(bodyBytes)))
			return
		}

		for _, link := range linksToCheck {
			// log.Debugf("adding %s to getLinks", link)
			if _, ok := r.linksExplored.get(link); !ok {
				r.linksExplored.put(link, "")
				r.getLinks <- link
			}
		}
	}
}

func (r *Racer) getLinksWorker() {
	defer r.wg.Done()

	for {
		select {
		case _ = <-r.done:
			return
		case linkToGet := <-r.getLinks:
			// log.Debugf("got %s from getLinks", linkToGet)
			u, err := url.Parse("https://en.wikipedia.org/w/api.php")
			if err != nil {
				r.handleGoroutineErr(errors.WithStack(err))
				return
			}

			// TODO: handle continues, what is batch complete?
			// moreResults := true
			// continueResult := ""
			// plcontinueResult := ""

			q := u.Query()
			q.Set("action", "query")
			q.Set("format", "json")
			q.Set("prop", "links")
			q.Set("titles", linkToGet)
			q.Set("redirects", "1")
			q.Set("formatversion", "2")
			q.Set("pllimit", "500")
			if rand.Intn(2) == 1 {
				q.Set("pldir", "descending")
			}

			// if len(continueResult) > 0 {
			// 	q.Set("continue", continueResult)
			// 	q.Set("plcontinue", plcontinueResult)
			// }

			u.RawQuery = q.Encode()

			resp, err := http.Get(u.String())
			if err != nil {
				r.handleGoroutineErr(errors.WithStack(err))
				return
			}
			bodyBytes, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				r.handleGoroutineErr(errors.WithStack(err))
				return
			}

			_, err = jsonparser.ArrayEach(bodyBytes, func(page []byte, dataType jsonparser.ValueType, offset int, err error) {
				parentPageTitle, err := jsonparser.GetString(page, "title")
				if err != nil {
					r.handleGoroutineErr(errors.WithStack(err))
					return
				}
				_, err = jsonparser.ArrayEach(page, func(link []byte, dataType jsonparser.ValueType, offset int, err error) {
					childPageTitle, err := jsonparser.GetString(link, "title")
					if err != nil {
						r.handleGoroutineErr(errors.WithStack(err))
						return
					}
					if _, ok := r.prevMap.get(childPageTitle); !ok {
						r.prevMap.put(childPageTitle, parentPageTitle)
						// log.Debugf("Adding %s to checkLinks", childPageTitle)
						r.checkLinks <- childPageTitle
					}
				}, "links")
				if err != nil {
					_, dataType, _, _ := jsonparser.Get(page, "links")
					if dataType != jsonparser.NotExist {
						r.handleGoroutineErr(errors.WithStack(err))
						return
					}
				}
			}, "query", "pages")
			if err != nil {
				r.handleGoroutineErr(errors.WithStack(err))
				return
			}

			// continueBlock, dataType, _, err := jsonparser.Get(bodyBytes, "continue")
			// if err != nil && dataType != jsonparser.NotExist {
			// 	r.handleGoroutineErr(errors.WithStack(err))
			// 	return
			// }
			// if len(continueBlock) == 0 {
			// 	moreResults = false
			// } else {
			// 	continueResult, err = jsonparser.GetString(bodyBytes, "continue", "continue")
			// 	if err != nil {
			// 		r.handleGoroutineErr(errors.WithStack(err))
			// 		return
			// 	}
			// 	plcontinueResult, err = jsonparser.GetString(bodyBytes, "continue", "plcontinue")
			// 	if err != nil {
			// 		r.handleGoroutineErr(errors.WithStack(err))
			// 		return
			// 	}
			// }
			// }
		}
	}
}

func (r *Racer) Run() ([]string, error) {
	r.prevMap.put(r.startTitle, "")
	r.linksExplored.put(r.startTitle, "")
	r.getLinks <- r.startTitle
	r.checkLinks <- r.startTitle
	for i := 0; i < numCheckLinksRoutines; i++ {
		r.wg.Add(1)
		go r.checkLinksWorker()
	}
	for i := 0; i < numGetLinksRoutines; i++ {
		r.wg.Add(1)
		go r.getLinksWorker()
	}
	r.wg.Wait()
	log.Debug("done waiting")
	log.Debugf("length of getLinks is %d and length of checkLinks is %d", len(r.getLinks), len(r.checkLinks))

	if r.err != nil {
		return nil, errors.WithStack(r.err)
	}

	finalPath := make([]string, 0)

	currentNode := r.endTitle
	for {
		// append to front
		finalPath = append([]string{currentNode}, finalPath...)
		nextNode, ok := r.prevMap.get(currentNode)

		if !ok || currentNode == r.startTitle {
			break
		}

		currentNode = nextNode
	}
	log.Debug("here")
	return finalPath, nil
}
