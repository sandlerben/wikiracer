// Package web implements http routing logic for the application.
package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	logMiddleware "github.com/bakins/logrus-middleware"
	"github.com/gorilla/mux"
	"github.com/sandlerben/wikiracer/race"
)

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
		raceHandler,
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

// healthHandler returns a 200 response to the client if the server is healthy.
func raceHandler(w http.ResponseWriter, r *http.Request) {
	startTitle := r.URL.Query().Get("starttitle")
	endTitle := r.URL.Query().Get("endtitle")
	if startTitle == "" || endTitle == "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, "Must pass start and end arguments.")
		return
	}
	racer := race.NewRacer(startTitle, endTitle)
	start := time.Now()
	path, err := racer.Run()
	elapsed := time.Since(start)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "An unexpected error has occurred:\n")
		io.WriteString(w, err.Error())
		return
	}
	io.WriteString(w, fmt.Sprintf("took %s\n", elapsed))
	io.WriteString(w, strings.Join(path, ","))
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
