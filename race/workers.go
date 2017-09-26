package race

import (
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/buger/jsonparser"
	"github.com/pkg/errors"
)

type workerType int

const (
	forwardType workerType = iota
	backwardType
)

var (
	forwardLinksChannelSize  = 10000000
	backwardLinksChannelSize = 10000000
)

type configuration struct {
	numForwardLinksRoutines  int
	numBackwardLinksRoutines int
	exploreAllLinks          bool
	exploreOnlyArticles      bool
}

// Config represents the configuration for this wikiracer.
var config configuration

func init() {
	config = configuration{
		numForwardLinksRoutines:  15,
		numBackwardLinksRoutines: 15,
		exploreAllLinks:          false,
		exploreOnlyArticles:      true,
	}

	var err error
	if numForwardLinksRoutines, ok := os.LookupEnv("NUM_FORWARD_LINKS_ROUTINES"); ok {
		config.numForwardLinksRoutines, err = strconv.Atoi(numForwardLinksRoutines)
	}
	if numBackwardLinksRoutines, ok := os.LookupEnv("NUM_BACKWARD_LINKS_ROUTINES"); ok {
		config.numBackwardLinksRoutines, err = strconv.Atoi(numBackwardLinksRoutines)
	}
	if exploreAllLinks, ok := os.LookupEnv("EXPLORE_ALL_LINKS"); ok {
		config.exploreAllLinks, err = strconv.ParseBool(exploreAllLinks)
	}
	// TODO: ADD TO README
	if exploreOnlyArticles, ok := os.LookupEnv("EXPLORE_ONLY_ARTICLES"); ok {
		config.exploreAllLinks, err = strconv.ParseBool(exploreOnlyArticles)
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

// higherOrderIteratePages returns a function which iterates through a `page`
// json blob for a worker. The function returned is compliant with the
// jsonparser.ArrayEach API.
//
// A higher order function is used here because the logic for forwardLinks
// and backwardLinks workers is extremely similar (with a few variables
// swapped.)
func (r *defaultRacer) higherOrderIteratePages(wType workerType) func([]byte, jsonparser.ValueType, int, error) {
	var mapFromMyComponent, mapFromOtherComponent *concurrentMap
	var myChan chan string
	var linksJSONKey string

	if wType == forwardType {
		mapFromMyComponent, mapFromOtherComponent = &r.pathFromStartMap, &r.pathFromEndMap
		myChan = r.forwardLinks
		linksJSONKey = "links"
	} else if wType == backwardType {
		mapFromMyComponent, mapFromOtherComponent = &r.pathFromEndMap, &r.pathFromStartMap
		myChan = r.backwardLinks
		linksJSONKey = "linkshere"
	}

	return func(page []byte, dataType jsonparser.ValueType, offset int, err error) {
		parentPageTitle, err := jsonparser.GetString(page, "title")
		if err != nil {
			r.handleErrInWorker(errors.WithStack(err))
			return
		}
		// the error here would just imply a missing key, it can be ignored
		missing, _ := jsonparser.GetBoolean(page, "missing")
		if missing {
			// this error should only end the race is it's caused by the user
			if parentPageTitle == r.startTitle || parentPageTitle == r.endTitle {
				r.handleErrInWorker(errors.Errorf("the page %s does not exist", parentPageTitle))
			}
			return
		}
		_, err = jsonparser.ArrayEach(page, func(link []byte, dataType jsonparser.ValueType, offset int, err error) {
			childPageTitle, err := jsonparser.GetString(link, "title")
			if err != nil {
				r.handleErrInWorker(errors.WithStack(err))
				return
			}
			if _, ok := mapFromOtherComponent.get(childPageTitle); ok {
				log.Debugf("found answer in worker! intersection at %s", childPageTitle)
				mapFromMyComponent.put(childPageTitle, parentPageTitle)

				r.meetingPoint.set(childPageTitle)

				r.closeOnce.Do(func() {
					close(r.done)
				}) // kill all goroutines
				return
			}
			_, childOk := mapFromMyComponent.get(childPageTitle)
			if !childOk && childPageTitle != parentPageTitle {
				mapFromMyComponent.put(childPageTitle, parentPageTitle)
				myChan <- childPageTitle
			}
		}, linksJSONKey)
		if err != nil {
			_, dataType, _, _ := jsonparser.Get(page, linksJSONKey)
			if dataType != jsonparser.NotExist {
				r.handleErrInWorker(errors.WithStack(err))
				return
			}
		}
	}
}

// forwardLinksWorker gets pages from forwardLinks and adds the pages linked from these
// pages to checkLinks.
func (r *defaultRacer) forwardLinksWorker() {
	for {
		select {
		case _ = <-r.done:
			return
		case linkToGet := <-r.forwardLinks:
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
			// q.Set("redirects", "1") // TODO
			q.Set("formatversion", "2")
			q.Set("pllimit", "500")
			if rand.Intn(2) == 1 { // let's mix things up a little
				q.Set("pldir", "descending")
			}
			if config.exploreOnlyArticles {
				q.Set("plnamespace", "0")
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
				// log.Debug(string(bodyBytes))

				_, err = jsonparser.ArrayEach(bodyBytes, r.higherOrderIteratePages(forwardType), "query", "pages")
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

// backwardLinksWorker gets pages from backwardLinks and adds the pages linked from these
// pages to checkLinks.
func (r *defaultRacer) backwardLinksWorker() {
	for {
		select {
		case _ = <-r.done:
			return
		case linkToGet := <-r.backwardLinks:
			u, err := url.Parse("https://en.wikipedia.org/w/api.php")
			if err != nil {
				r.handleErrInWorker(errors.WithStack(err))
				return
			}

			// the wikimedia API sometimes doesn't return all results in one response.
			// these variables allow the client to query for more results.
			moreResults := true
			continueResult := ""
			lhcontinueResult := ""

			q := u.Query()
			q.Set("action", "query")
			q.Set("format", "json")
			q.Set("prop", "linkshere")
			q.Set("lhprop", "title")
			q.Set("titles", linkToGet)
			q.Set("formatversion", "2")
			q.Set("lhlimit", "500")
			if config.exploreOnlyArticles {
				q.Set("lhnamespace", "0")
			}

			for moreResults {

				if len(continueResult) > 0 {
					q.Set("continue", continueResult)
					q.Set("lhcontinue", lhcontinueResult)
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

				_, err = jsonparser.ArrayEach(bodyBytes, r.higherOrderIteratePages(backwardType), "query", "pages")
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
					lhcontinueResult, err = jsonparser.GetString(bodyBytes, "continue", "lhcontinue")
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
