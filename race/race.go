// TODO: document this package
package race

import (
	"io/ioutil"
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
	startTitle string
	endTitle   string
	prevMap    concurrentMap
	visitedMap concurrentMap
	wg         sync.WaitGroup
	checkLinks chan string
	getLinks   chan string
	done       chan bool
	err        error
}

// what do we do on success? how is that passed? and error?

var (
	checkLinksSize        = 50
	getLinksSize          = 5
	numCheckLinksRoutines = 5
	numGetLinksRoutines   = 5
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
	// seen set
	r.visitedMap = concurrentMap{m: make(map[string]string)}

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

	close(r.done) // kill all goroutines
}

func (r *Racer) checkLinksWorker() {
	defer r.wg.Done()

	for {
		linksToCheck := make([]string, checkLinksSize)
		for i := 0; i < checkLinksSize; i++ {
			select {
			case _ = <-r.done:
				return
			case link := <-r.checkLinks:
				linksToCheck = append(linksToCheck, link)
			}
		}

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

		// TODO: handle continues, what is batch complete?
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

				r.prevMap.put(r.endTitle, prevName)
				close(r.done)
				return
			}
		}, "query", "pages")
		if err != nil {
			r.handleGoroutineErr(errors.WithStack(err))
			return
		}
		for _, link := range linksToCheck {
			r.getLinks <- link
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
			u, err := url.Parse("https://en.wikipedia.org/w/api.php")
			if err != nil {
				r.handleGoroutineErr(errors.WithStack(err))
				return
			}

			// TODO: handle continues, what is batch complete?
			moreResults := true
			continueResult := ""
			plcontinueResult := ""

			for moreResults {
				q := u.Query()
				q.Set("action", "query")
				q.Set("format", "json")
				q.Set("prop", "links")
				q.Set("titles", linkToGet)
				q.Set("redirects", "1")
				q.Set("formatversion", "2")
				q.Set("pllimit", "500")

				if len(continueResult) > 0 {
					q.Set("continue", continueResult)
					q.Set("plcontinue", plcontinueResult)
				}

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
						r.prevMap.put(childPageTitle, parentPageTitle)
						if _, ok := r.visitedMap.get(childPageTitle); !ok {
							r.visitedMap.put(childPageTitle, "")
							r.checkLinks <- childPageTitle
						}
					}, "links")
					if err != nil {
						r.handleGoroutineErr(errors.WithStack(err))
						return
					}
				}, "query", "pages")
				if err != nil {
					r.handleGoroutineErr(errors.WithStack(err))
					return
				}

				continueBlock, dataType, _, err := jsonparser.Get(bodyBytes, "continue")
				if err != nil && dataType != jsonparser.NotExist {
					r.handleGoroutineErr(errors.WithStack(err))
					return
				}
				if len(continueBlock) == 0 {
					moreResults = false
				} else {
					continueResult, err = jsonparser.GetString(bodyBytes, "continue", "continue")
					if err != nil {
						r.handleGoroutineErr(errors.WithStack(err))
						return
					}
					plcontinueResult, err = jsonparser.GetString(bodyBytes, "continue", "plcontinue")
					if err != nil {
						r.handleGoroutineErr(errors.WithStack(err))
						return
					}
				}
			}
		}
	}
}

func (r *Racer) Run() ([]string, error) {
	r.visitedMap.put(r.startTitle, "")
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

	if r.err != nil {
		return nil, errors.WithStack(r.err)
	}

	finalPath := make([]string, 0)

	currentNode := r.endTitle
	for {
		if nextNode, ok := r.prevMap.get(currentNode); ok {
			// append to front
			finalPath = append([]string{currentNode}, finalPath...)
			currentNode = nextNode
		} else {
			break
		}
	}
	return finalPath, nil
}
