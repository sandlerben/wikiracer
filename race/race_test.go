package race

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	httpmock "gopkg.in/jarcoal/httpmock.v1"
)

var (
	checkLinksResponse = `{"batchcomplete":true,"query":{"pages":[{"pageid":8569916,"ns":0,"title":"English language","links":[{"ns":0,"title":"end"}]}]}}`
	getLinksResponse   = `{"query":{"pages":[{"pageid":8569916,"ns":0,"title":"start","links":[{"ns":14,"title":"English language"},{"ns":14,"title":"Spanish language"},{"ns":14,"title":"French language"},{"ns":14,"title":"German language"}]}}`
)

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

func getCheckLinksURL(samplePages []string) string {
	u, _ := url.Parse("https://en.wikipedia.org/w/api.php")
	q := u.Query()
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("prop", "links")
	q.Set("titles", strings.Join(samplePages, "|"))
	q.Set("redirects", "1")
	q.Set("formatversion", "2")
	q.Set("pllimit", "500")
	q.Set("pltitles", "end")
	u.RawQuery = q.Encode()
	return u.String()
}

func TestCheckLinksWorker(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	samplePages := []string{"one", "two", "three", "four"}
	u := getCheckLinksURL(samplePages)
	httpmock.RegisterResponder("GET", u,
		httpmock.NewStringResponder(200, checkLinksResponse))

	r := newDefaultRacer("start", "end", 1*time.Minute)

	for _, page := range samplePages {
		r.checkLinks <- page
	}

	go r.checkLinksWorker()
	_ = <-r.done // we will only go past this line if checkLinksWorker closes done

	for _, page := range samplePages {
		if _, ok := r.linksExplored.get(page); !ok {
			t.Errorf("link %s should be in linksExplored but it is not", page)
		}
	}
}

func TestCheckLinksWorkerHandleErr(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	samplePages := []string{"one", "two", "three", "four"}
	u := getCheckLinksURL(samplePages)
	httpmock.RegisterResponder("GET", u,
		httpmock.NewStringResponder(200, `{"batchcomplete":true}`))

	r := newDefaultRacer("start", "end", 1*time.Minute)

	for _, page := range samplePages {
		r.checkLinks <- page
	}

	go r.checkLinksWorker()
	_ = <-r.done // we will only go past this line if checkLinksWorker closes done

	for _, page := range samplePages {
		if _, ok := r.linksExplored.get(page); ok {
			t.Errorf("link %s should not be in linksExplored but it is", page)
		}
	}

	if r.err == nil {
		t.Errorf("an error should have been caught")
	}

	_ = <-r.done // done must be closed. if done wasn't closed, this will hang.
}

func getGetLinksURL(linkToGet string) *url.URL {
	u, _ := url.Parse("https://en.wikipedia.org/w/api.php")
	q := u.Query()
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("prop", "links")
	q.Set("titles", linkToGet)
	q.Set("redirects", "1")
	q.Set("formatversion", "2")
	q.Set("pllimit", "500")
	q.Set("pldir", "descending")
	return u
}

func TestGetLinksWorker(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	linkToGet := "one"
	u := getGetLinksURL(linkToGet)
	httpmock.RegisterResponder("GET", u.String(),
		httpmock.NewStringResponder(200, getLinksResponse))
	u.Query().Del("pldir")
	httpmock.RegisterResponder("GET", u.String(),
		httpmock.NewStringResponder(200, getLinksResponse))

	r := newDefaultRacer("start", "end", 1*time.Minute)

	r.getLinks <- linkToGet

	r.getLinksWorker()

	samplePages := []string{"English language", "French language", "Spanish language", "German language"}
	for _, page := range samplePages {
		if _, ok := r.prevMap.get(page); !ok {
			t.Errorf("link %s should be in prevMap but it is not", page)
		}
	}
}

func TestGetLinksWorkerHandleErr(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	linkToGet := "one"
	u := getGetLinksURL(linkToGet)
	httpmock.RegisterResponder("GET", u.String(),
		httpmock.NewStringResponder(200, `{"batchcomplete":true}`))
	u.Query().Del("pldir")
	httpmock.RegisterResponder("GET", u.String(),
		httpmock.NewStringResponder(200, `{"batchcomplete":true}`))

	r := newDefaultRacer("start", "end", 1*time.Minute)

	r.getLinks <- linkToGet

	go r.getLinksWorker()
	_ = <-r.done // we will only go past this line if getLinksWorker closes done

	samplePages := []string{"English language", "French language", "Spanish language", "German language"}
	for _, page := range samplePages {
		if _, ok := r.prevMap.get(page); ok {
			t.Errorf("link %s should not be in prevMap but it is", page)
		}
	}
}
