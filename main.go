// Package main initializes a web server.
package main

import (
	"fmt"
	"net/http"
	_ "net/http/pprof" // import for side effects
	"os"

	log "github.com/Sirupsen/logrus"
	"github.com/sandlerben/wikiracer/web"
)

func init() {
	log.SetOutput(os.Stderr)
	log.SetLevel(log.DebugLevel)
}

func main() {
	router := web.NewRouter()
	middlewareRouter := web.ApplyMiddleware(router)

	// serve http
	http.Handle("/", middlewareRouter)

	log.Infof("Server is running at http://localhost:%d", 8000)
	addr := fmt.Sprintf(":%d", 8000)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Error(err)
	}
}
