// Package main initializes a web server.
package main

import (
	"fmt"
	"net/http"
	_ "net/http/pprof" // import for side effects
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/sandlerben/wikiracer/web"
)

func init() {
	log.SetOutput(os.Stderr)
	log.SetLevel(log.InfoLevel)
}

func main() {
	router := web.NewRouter()
	middlewareRouter := web.ApplyMiddleware(router)

	// serve http
	http.Handle("/", middlewareRouter)

	var port string
	var ok bool
	if port, ok = os.LookupEnv("WIKIRACER_PORT"); !ok {
		port = "8000"
	}
	log.Infof("Server is running at http://localhost:%s", port)
	addr := fmt.Sprintf(":%s", port)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Error(err)
	}
}
