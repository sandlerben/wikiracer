package race

import (
	"math/rand"
	"net/http"
	"net/url"
	"testing"
	"time"

	httpmock "gopkg.in/jarcoal/httpmock.v1"
)

var (
	forwardLinksResponse  = `{"query":{"pages":[{"pageid":8569916,"ns":0,"title":"start","links":[{"ns":14,"title":"English language"},{"ns":14,"title":"Spanish language"},{"ns":14,"title":"French language"},{"ns":14,"title":"German language"}]}]}`
	backwardLinksResponse = `{"query":{"pages":[{"pageid":8569916,"ns":0,"title":"end","linkshere":[{"ns":14,"title":"English language"},{"ns":14,"title":"Spanish language"},{"ns":14,"title":"French language"},{"ns":14,"title":"German language"}]}]}`

	forwardLinksResponseWithContinue  = `{"continue": {"plcontinue": "39027|0|Shawn_Michaels","continue": "||"},"query":{"pages":[{"pageid":8569916,"ns":0,"title":"start","links":[{"ns":14,"title":"Hebrew language"}]}]}`
	backwardLinksResponseWithContinue = `{"continue": {"plcontinue": "39027|0|Shawn_Michaels","continue": "||"},"query":{"pages":[{"pageid":8569916,"ns":0,"title":"end","linkshere":[{"ns":14,"title":"Hebrew language"}]}]}`
)

func init() {
	// mock the randomness of workers.go
	rand.Seed(0)
}

func TestLoopUntilResponse(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	requestsMadeSoFar := 0
	httpmock.RegisterResponder("GET", "http://example.com",
		func(req *http.Request) (*http.Response, error) {
			var resp *http.Response
			if requestsMadeSoFar < 2 {
				resp = httpmock.NewStringResponse(429, "try again")
			} else {
				resp = httpmock.NewStringResponse(200, "good job")
			}
			requestsMadeSoFar++
			return resp, nil
		})

	r := newDefaultRacer("start", "end", 1*time.Minute)
	u, _ := url.Parse("http://example.com")
	resp, err := r.loopUntilResponse(u)
	if err != nil {
		t.Error(err)
	} else {
		if resp.StatusCode != 200 {
			t.Error("http code must be 200")
		}
	}
}

func getForwardLinksURL(linkToGet string) *url.URL {
	u, _ := url.Parse("https://en.wikipedia.org/w/api.php")
	q := u.Query()
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("prop", "links")
	q.Set("titles", linkToGet)
	q.Set("formatversion", "2")
	q.Set("pllimit", "500")
	q.Set("pldir", "descending")
	q.Set("plnamespace", "0")
	return u
}

func TestForwardLinksWorker(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	linkToGet := "one"
	u := getForwardLinksURL(linkToGet)
	httpmock.RegisterResponder("GET", u.String(),
		httpmock.NewStringResponder(200, forwardLinksResponse))

	r := newDefaultRacer("start", "German language", 1*time.Minute)
	r.pathFromEndMap.put("German language", "")

	r.forwardLinks <- linkToGet
	go r.forwardLinksWorker()
	_ = <-r.done // we will only go past this line if forwardLinksWorker closes done

	samplePages := []string{"English language", "French language", "Spanish language", "German language"}
	for _, page := range samplePages {
		if _, ok := r.pathFromStartMap.get(page); !ok {
			t.Errorf("link %s should be in pathFromStartMap but it is not", page)
		}
	}
}

func TestForwardLinksWorkerHandleErr(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	linkToGet := "one"
	u := getForwardLinksURL(linkToGet)
	httpmock.RegisterResponder("GET", u.String(),
		httpmock.NewStringResponder(200, `{"batchcomplete":true}`))

	r := newDefaultRacer("start", "end", 1*time.Minute)

	r.forwardLinks <- linkToGet
	go r.forwardLinksWorker()
	_ = <-r.done // we will only go past this line if forwardLinksWorker closes done

	samplePages := []string{"English language", "French language", "Spanish language", "German language"}
	for _, page := range samplePages {
		if _, ok := r.pathFromStartMap.get(page); ok {
			t.Errorf("link %s should not be in pathFromStartMap but it is", page)
		}
	}
}

func getBackwardLinksURL(linkToGet string) *url.URL {
	u, _ := url.Parse("https://en.wikipedia.org/w/api.php")
	q := u.Query()
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("prop", "linkshere")
	q.Set("lhprop", "title")
	q.Set("titles", linkToGet)
	q.Set("formatversion", "2")
	q.Set("lhlimit", "500")
	q.Set("lhnamespace", "0")
	return u
}

func TestBackwardLinksWorker(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	linkToGet := "one"
	u := getBackwardLinksURL(linkToGet)
	httpmock.RegisterResponder("GET", u.String(),
		httpmock.NewStringResponder(200, backwardLinksResponse))

	r := newDefaultRacer("German language", "end", 1*time.Minute)
	r.pathFromStartMap.put("German language", "")

	r.backwardLinks <- linkToGet
	go r.backwardLinksWorker()
	_ = <-r.done // we will only go past this line if backwardLinksWorker closes done

	samplePages := []string{"English language", "French language", "Spanish language", "German language"}
	for _, page := range samplePages {
		if _, ok := r.pathFromEndMap.get(page); !ok {
			t.Errorf("link %s should be in pathFromStartMap but it is not", page)
		}
	}
}

func TestBackwardLinksWorkerHandleErr(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	linkToGet := "one"
	u := getBackwardLinksURL(linkToGet)
	httpmock.RegisterResponder("GET", u.String(),
		httpmock.NewStringResponder(200, `{"batchcomplete":true}`))

	r := newDefaultRacer("start", "end", 1*time.Minute)

	r.backwardLinks <- linkToGet
	go r.backwardLinksWorker()
	_ = <-r.done // we will only go past this line if backwardLinksWorker closes done

	samplePages := []string{"English language", "French language", "Spanish language", "German language"}
	for _, page := range samplePages {
		if _, ok := r.pathFromEndMap.get(page); ok {
			t.Errorf("link %s should not be in pathFromStartMap but it is", page)
		}
	}
}
