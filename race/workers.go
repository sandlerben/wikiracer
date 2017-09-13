package race

import (
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

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

// loopUntilResponse makes requests to the MediaWiki API until it does not get
// code=429 "Too Many Requests"
func (r *defaultRacer) loopUntilResponse(u *url.URL) (*http.Response, error) {
	var resp *http.Response
	for {
		var err error
		resp, err = http.Get(u.String())
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == 429 {
			time.Sleep(time.Millisecond * 100)
		} else {
			break
		}
	}
	return resp, nil
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

		resp, err := r.loopUntilResponse(u)
		if err != nil {
			r.handleErrInWorker(err)
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

				resp, err := r.loopUntilResponse(u)
				if err != nil {
					r.handleErrInWorker(err)
					return
				}
				bodyBytes, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					r.handleErrInWorker(errors.WithStack(err))
					return
				}

				_, err = jsonparser.ArrayEach(bodyBytes, r.getLinksIteratePages, "query", "pages")
				if err != nil {
					r.handleErrInWorker(errors.Wrap(err, string(bodyBytes)))
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

func (r *defaultRacer) giveUpAfterTime(timer *time.Timer) {
	select {
	case _ = <-r.done:
		return
	case _ = <-timer.C:
		r.closeOnce.Do(func() {
			close(r.done) // kill all goroutines
		})
	}
}