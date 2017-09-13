// Package web implements http routing logic for the application.
package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	logMiddleware "github.com/bakins/logrus-middleware"
	"github.com/gorilla/mux"
	"github.com/sandlerben/wikiracer/race"
)

var requestCache map[requestInfo][]string
var timeLimit time.Duration

func init() {
	requestCache = make(map[requestInfo][]string)

	if timeLimitString, ok := os.LookupEnv("WIKIRACER_TIME_LIMIT"); ok {
		var err error
		if timeLimit, err = time.ParseDuration(timeLimitString); err != nil {
			log.Panic(err)
		}
	} else {
		timeLimit = 1 * time.Minute
	}
}

type route struct {
	Name        string
	Method      string
	Pattern     string
	HandlerFunc http.HandlerFunc
}

var routes = []route{
	route{
		"race",
		"GET",
		"/race",
		raceHandler(race.NewRacer),
	},
	route{
		"health",
		"GET",
		"/health",
		healthHandler,
	},
}

// healthHandler returns a 200 response to the client if the server is healthy.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, "OK :)")
}

type requestInfo struct {
	startTitle string
	endTitle   string
}

// raceHandler returns a handler for the race endpoint which uses the supplied
// race.Racer. The raceHandler is parameterized in this way to enable mock
// testing.
func raceHandler(newRacer func(a, b string, c time.Duration) race.Racer) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		startTitle := r.URL.Query().Get("starttitle")
		endTitle := r.URL.Query().Get("endtitle")
		forceNoCache := r.URL.Query().Get("nocache")
		if startTitle == "" || endTitle == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, "Must pass start and end arguments.")
			return
		} else if startTitle == endTitle {
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, "starttitle cannot equal endtitle")
			return
		}
		racer := newRacer(startTitle, endTitle, timeLimit)
		start := time.Now()
		currentRequestInfo := requestInfo{startTitle: startTitle, endTitle: endTitle}

		path, ok := requestCache[currentRequestInfo]
		if !ok || forceNoCache == "1" {
			var err error
			path, err = racer.Run()
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				io.WriteString(w, "An unexpected error has occurred:\n")
				io.WriteString(w, err.Error())
				return
			}
			if path != nil {
				requestCache[currentRequestInfo] = path
			}
		}

		elapsed := time.Since(start)
		var output map[string]interface{}
		if path != nil {
			output = map[string]interface{}{
				"path":       path,
				"time_taken": elapsed.String(),
			}
		} else {
			output = map[string]interface{}{
				"path":       []string{},
				"message":    fmt.Sprintf("no path found within %s", timeLimit),
				"time_taken": timeLimit.String(),
			}
		}
		jsonOutput, err := json.MarshalIndent(output, "", "    ")
		if err != nil {
			log.Panic(err)
		}
		w.Write(jsonOutput)
	}
}

// NewRouter creates and returns a mux.Router with default routes.
func NewRouter() *mux.Router {
	router := mux.NewRouter()

	for _, route := range routes {
		router.
			Methods(route.Method).
			Path(route.Pattern).
			Name(route.Name).
			Handler(route.HandlerFunc)
	}

	return router
}

// ApplyMiddleware wraps the router in some middleware. This middleware includes
// logging and gzip compression.
func ApplyMiddleware(router http.Handler) http.Handler {
	loggingHandler := func(h http.Handler) http.Handler {
		m := new(logMiddleware.Middleware)
		return m.Handler(h, "")
	}
	middlewareRouter := loggingHandler(router)
	return middlewareRouter
}
