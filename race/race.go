// Package race encapsulates the logic for executing a wikirace.
package race

import (
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/buger/jsonparser"
	"github.com/pkg/errors"
)

var (
	checkLinksSize = 10000000
	getLinksSize   = 10000000
)

type configuration struct {
	numCheckLinksRoutines int
	numGetLinksRoutines   int
	exploreAllLinks       bool
}

// Config represents the configuration for this wikiracer.
var config configuration

func init() {
	config = configuration{
		numCheckLinksRoutines: 10,
		numGetLinksRoutines:   5,
		exploreAllLinks:       true,
	}

	var err error
	if numCheckLinksRoutines, ok := os.LookupEnv("NUM_CHECK_LINKS_ROUTINES"); ok {
		config.numCheckLinksRoutines, err = strconv.Atoi(numCheckLinksRoutines)
	}
	if numGetLinksRoutines, ok := os.LookupEnv("NUM_GET_LINKS_ROUTINES"); ok {
		config.numGetLinksRoutines, err = strconv.Atoi(numGetLinksRoutines)
	}
	if exploreAllLinks, ok := os.LookupEnv("EXPLORE_ALL_LINKS"); ok {
		config.exploreAllLinks, err = strconv.ParseBool(exploreAllLinks)
	}
	if err != nil {
		log.Panic(err)
	}
}

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
	err           error         // err that should be passed back to requester
}

func newDefaultRacer(startTitle string, endTitle string) *defaultRacer {
	r := new(defaultRacer)
	r.startTitle = startTitle
	r.endTitle = endTitle
	r.prevMap = concurrentMap{m: make(map[string]string)}
	r.linksExplored = concurrentMap{m: make(map[string]string)}
	r.checkLinks = make(chan string, checkLinksSize)
	r.getLinks = make(chan string, getLinksSize)
	r.done = make(chan bool, 1)
	return r
}

// NewRacer returns a Racer which can run a race from start to end.
func NewRacer(startTitle string, endTitle string) Racer {
	return newDefaultRacer(startTitle, endTitle)
}

// handleErrInWorker contains common error handling logic for when an error
// occurs in a worker goroutine
func (r *defaultRacer) handleErrInWorker(err error) {
	log.Error("err occurred in worker")
	log.Errorf("%+v", err)
	r.err = err

	r.closeOnce.Do(func() {
		close(r.done) // kill all goroutines
	})
}

// checkLinksIteratePages is a function which iterates through a `page` json
// blob for checkLinks. It is compliant with the jsonparser.ArrayEach API.
func (r *defaultRacer) checkLinksIteratePages(page []byte, dataType jsonparser.ValueType, offset int, err error) {
	linksData, dataType, _, err := jsonparser.Get(page, "links")
	if err != nil && dataType != jsonparser.NotExist {
		r.handleErrInWorker(errors.WithStack(err))
		return
	}
	if len(linksData) > 0 { // found it!
		// figure out the page that got us there
		prevName, err := jsonparser.GetString(page, "title")
		if err != nil {
			r.handleErrInWorker(errors.WithStack(err))
			return
		}
		log.Debugf("found %s via %s", string(linksData), prevName)

		r.prevMap.put(r.endTitle, prevName)
		r.closeOnce.Do(func() {
			close(r.done)
		}) // kill all goroutines
		return
	}
}

// getLinksIteratePages is a function which iterates through a `page` json
// blob for getLinks. It is compliant with the jsonparser.ArrayEach API.
func (r *defaultRacer) getLinksIteratePages(page []byte, dataType jsonparser.ValueType, offset int, err error) {
	parentPageTitle, err := jsonparser.GetString(page, "title")
	if err != nil {
		r.handleErrInWorker(errors.WithStack(err))
		return
	}
	_, err = jsonparser.ArrayEach(page, func(link []byte, dataType jsonparser.ValueType, offset int, err error) {
		childPageTitle, err := jsonparser.GetString(link, "title")
		if err != nil {
			r.handleErrInWorker(errors.WithStack(err))
			return
		}
		_, childOk := r.prevMap.get(childPageTitle)
		if !childOk && childPageTitle != parentPageTitle {
			r.prevMap.put(childPageTitle, parentPageTitle)
			r.checkLinks <- childPageTitle
		}
	}, "links")
	if err != nil {
		_, dataType, _, _ := jsonparser.Get(page, "links")
		if dataType != jsonparser.NotExist {
			r.handleErrInWorker(errors.WithStack(err))
			return
		}
	}
}

// checkLinksWorker gets links from linksToCheck and checks if any of them are
// adjacent to end. If one is, the worker alerts all other workers.
func (r *defaultRacer) checkLinksWorker() {
	for {
		linksToCheck := make([]string, 0)

	linksToCheckLoop:
		for len(linksToCheck) < 50 {
			select {
			case _ = <-r.done:
				return
			case link := <-r.checkLinks:
				linksToCheck = append(linksToCheck, link)
			default: // nothing to read on channel
				if len(linksToCheck) > 0 { // if we have at least 1, let's boogie
					break linksToCheckLoop
				}
			}
		}
		u, err := url.Parse("https://en.wikipedia.org/w/api.php")
		if err != nil {
			r.handleErrInWorker(errors.WithStack(err))
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
		q.Set("pltitles", r.endTitle)
		u.RawQuery = q.Encode()

		resp, err := http.Get(u.String())
		if err != nil {
			r.handleErrInWorker(errors.WithStack(err))
			return
		}
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			r.handleErrInWorker(errors.WithStack(err))
			return
		}

		_, err = jsonparser.ArrayEach(bodyBytes, r.checkLinksIteratePages, "query", "pages")
		if err != nil {
			r.handleErrInWorker(errors.Wrap(err, string(bodyBytes)))
			return
		}

		for _, link := range linksToCheck {
			// if this link hasn't been explored yet, let's explore it.
			if _, ok := r.linksExplored.get(link); !ok {
				r.linksExplored.put(link, "")
				r.getLinks <- link
			}
		}
	}
}

// getLinksWorker gets pages from getLinks and adds the pages linked from these
// pages to checkLinks.
func (r *defaultRacer) getLinksWorker() {
	for {
		select {
		case _ = <-r.done:
			return
		case linkToGet := <-r.getLinks:
			u, err := url.Parse("https://en.wikipedia.org/w/api.php")
			if err != nil {
				r.handleErrInWorker(errors.WithStack(err))
				return
			}

			// the wikimedia API sometimes doesn't return all results in one response.
			// these variables allow the client to query for more results.
			moreResults := true
			continueResult := ""
			plcontinueResult := ""

			q := u.Query()
			q.Set("action", "query")
			q.Set("format", "json")
			q.Set("prop", "links")
			q.Set("titles", linkToGet)
			q.Set("redirects", "1")
			q.Set("formatversion", "2")
			q.Set("pllimit", "500")
			if rand.Intn(2) == 1 { // let's mix things up a little
				q.Set("pldir", "descending")
			}

			for moreResults {

				if len(continueResult) > 0 {
					q.Set("continue", continueResult)
					q.Set("plcontinue", plcontinueResult)
				}

				u.RawQuery = q.Encode()

				resp, err := http.Get(u.String())
				if err != nil {
					r.handleErrInWorker(errors.WithStack(err))
					return
				}
				bodyBytes, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					r.handleErrInWorker(errors.WithStack(err))
					return
				}

				_, err = jsonparser.ArrayEach(bodyBytes, r.getLinksIteratePages, "query", "pages")
				if err != nil {
					r.handleErrInWorker(errors.WithStack(err))
					return
				}

				continueBlock, dataType, _, err := jsonparser.Get(bodyBytes, "continue")
				if err != nil && dataType != jsonparser.NotExist {
					r.handleErrInWorker(errors.WithStack(err))
					return
				}
				if len(continueBlock) == 0 || !config.exploreAllLinks {
					moreResults = false
				} else {
					continueResult, err = jsonparser.GetString(bodyBytes, "continue", "continue")
					if err != nil {
						r.handleErrInWorker(errors.WithStack(err))
						return
					}
					plcontinueResult, err = jsonparser.GetString(bodyBytes, "continue", "plcontinue")
					if err != nil {
						r.handleErrInWorker(errors.WithStack(err))
						return
					}
				}
			}
		}
	}
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
	_ = <-r.done

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
	log.Debugf("at end of Run(), checkLinks length is %d, getLinks length is %d", len(r.checkLinks), len(r.getLinks))
	return finalPath, nil
}
