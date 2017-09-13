package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sandlerben/wikiracer/mocks"
	"github.com/sandlerben/wikiracer/race"
)

// Note: The tests in this file were informed by (and partially copied from)
// this tutorial on httptest: https://elithrar.github.io/article/testing-http-handlers-go/

func TestHealthHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/health", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(healthHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	// Check the response body is what we expect.
	expected := "OK :)"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v",
			rr.Body.String(), expected)
	}
}

func TestRaceHandlerMissingArgsError(t *testing.T) {
	requestCache = make(map[requestInfo][]string)
	req, err := http.NewRequest("GET", "/race?starttitle=start", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	mockNewRacer := func(a, b string, c time.Duration) race.Racer {
		return new(mocks.Racer)
	}
	handler := http.HandlerFunc(raceHandler(mockNewRacer))

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusUnprocessableEntity {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusUnprocessableEntity)
	}
}

func TestRaceHandlerErrorPropogated(t *testing.T) {
	requestCache = make(map[requestInfo][]string)
	req, err := http.NewRequest("GET", "/race?starttitle=start&endtitle=end", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	mockRacer := new(mocks.Racer)
	newRacer := func(a, b string, c time.Duration) race.Racer {
		return mockRacer
	}
	handler := http.HandlerFunc(raceHandler(newRacer))
	mockRacer.On("Run").Return(nil, errors.New("sample error"))

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusUnprocessableEntity)
	}

	mockRacer.AssertNumberOfCalls(t, "Run", 1)
}

func TestRaceHandlerNothingInCache(t *testing.T) {
	requestCache = make(map[requestInfo][]string)
	req, err := http.NewRequest("GET", "/race?starttitle=start&endtitle=end", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	mockRacer := new(mocks.Racer)
	newRacer := func(a, b string, c time.Duration) race.Racer {
		return mockRacer
	}
	handler := http.HandlerFunc(raceHandler(newRacer))
	mockRacer.On("Run").Return([]string{"start", "middle", "end"}, nil)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusUnprocessableEntity)
	}

	mockRacer.AssertNumberOfCalls(t, "Run", 1)
}

func TestRaceHandlerNothingInCacheNoPathReturned(t *testing.T) {
	requestCache = make(map[requestInfo][]string)
	req, err := http.NewRequest("GET", "/race?starttitle=start&endtitle=end", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	mockRacer := new(mocks.Racer)
	newRacer := func(a, b string, c time.Duration) race.Racer {
		return mockRacer
	}
	handler := http.HandlerFunc(raceHandler(newRacer))
	mockRacer.On("Run").Return(nil, nil)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusUnprocessableEntity)
	}

	mockRacer.AssertNumberOfCalls(t, "Run", 1)
}

func TestRaceHandlerPathInCache(t *testing.T) {
	requestCache = make(map[requestInfo][]string)
	info := requestInfo{
		startTitle: "start",
		endTitle:   "end",
	}
	requestCache[info] = []string{"start", "middle", "end"}

	req, err := http.NewRequest("GET", "/race?starttitle=start&endtitle=end", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	mockRacer := new(mocks.Racer)
	newRacer := func(a, b string, c time.Duration) race.Racer {
		return mockRacer
	}
	handler := http.HandlerFunc(raceHandler(newRacer))

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusUnprocessableEntity)
	}

	mockRacer.AssertNotCalled(t, "Run")
}

func TestRaceHandlerForceIgnoreCache(t *testing.T) {
	requestCache = make(map[requestInfo][]string)
	info := requestInfo{
		startTitle: "start",
		endTitle:   "end",
	}
	requestCache[info] = []string{"start", "middle", "end"}

	req, err := http.NewRequest("GET", "/race?starttitle=start&endtitle=end&nocache=1", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	mockRacer := new(mocks.Racer)
	newRacer := func(a, b string, c time.Duration) race.Racer {
		return mockRacer
	}
	handler := http.HandlerFunc(raceHandler(newRacer))
	mockRacer.On("Run").Return([]string{"start", "middle", "end"}, nil)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusUnprocessableEntity)
	}

	mockRacer.AssertNumberOfCalls(t, "Run", 1)
}
