# wikiracer

## Synopsis

Go application which finds a path from one Wikipedia page to another by following links.

## Basic Usage

Start a wikiracer server on port `8000` (the port can be customized with `WIKIRACER_PORT`)

```
$ wikiracer
```

In order to initiate a race, make a `GET` request to the server's `/race` endpoint with the following arguments.

- starttitle **(required)**: The wikipedia page to start from.
- endtitle **(required)**: The wikipedia page to find a path to.
- nocache: By default, the server caches all paths found from start to end. To ignore the cache for this race, set `nocache=1`.

The endpoint returns a path from the start page to the end page and how long it took to find the path.

### Customizing behavior

The following environment variables can be set to customize the behavior of wikiracer.

- `WIKIRACER_PORT`: The port on which to run a HTTP server (default `8000`).
- `NUM_CHECK_LINKS_ROUTINES`: The number of concurrent checkLinks workers to run (default 2).
- `NUM_GET_LINKS_ROUTINES`: The number of concurrent getLinks workers to run (default 2).
- `EXPLORE_ALL_LINKS`: Sometimes, the MediaWiki API doesn't return all links in once response. As a result, wikiracer continues to query the MediaWiki API until all the links are returned. If `EXPLORE_ALL_LINKS` is set to `"false"`, then wikiracer will not continue even if there are more links.

## Installation

Create a directory and clone the repo:

```
$ mkdir $GOPATH/src/github.com/sandlerben/wikiracer
$ cd $GOPATH/src/github.com/sandlerben/wikiracer
$ git clone https://github.com/sandlerben/wikiracer.git .
```

Install the dependencies using [dep](https://github.com/golang/dep), the semi-official Go dependency management tool.

```
$ dep ensure
```

Finally, install wikiracer.

```
go install
```

## Why Go?

I wrote this application in Go for a few reasons:

- I have experience using Go in the past from [go-torch](https://github.com/uber/go-torch) and [transcribe4all](https://github.com/hack4impact/transcribe4all).
- Go has extremely powerful concurrency primitives ([goroutines](https://gobyexample.com/goroutines) and [channels](https://tour.golang.org/concurrency/2)) and an excellent runtime/scheduler which make writing fast, thread-safe concurrent programs a breeze.
- Go has a great standard library. In particular, it's easy to create a full-featured HTTP server and make HTTP requests with Go's built-ins.

## Architecture overview

[![wikiracer overview](./wikiracer_overview.svg)](./wikiracer_overview.svg)

### Web

The `wikiracer/web` package encapsulates the logic for handling HTTP requests. The package exposes two endpoints:

- `/race` returns a path from a start page to an end page.
- `/health` returns a message indicating that the server is alive and healthy.

The `wikiracer/web` package uses the `gorilla/mux` router, an extremely popular Go URL dispatcher.

The `wikiracer/web` package also features a simple cache of paths previously found (implemented as a Go map). That way, if a request with the same start and end pages is made multiple times, the wikipedia graph only needs to be traversed once.

### Race

The `wikiracer/race` encapsulates the wikipedia exploring logic; it is the most interesting part of the application.

A `race.Racer` struct encapsulates all the state needed for one race, including:

- The page to start at
- The page to find a path to
- A record of how each page was reached during the race
- A record of the links explored so far
- Several synchronization primitives (including `sync.WaitGroup` and `sync.Once`)
- Channels to allow concurrent workers to communicate  

A `race.Racer` exposes one public function, `Run`, which returns a path from start to end to the caller.

Under the hood, there is a lot going on inside the `race` package. Specifically, a number of `checkLinks` workers and `getLinks` workers concurrently do work while communicating with each other (via channels) until a path to the end page is found.

At a high level, links flow through a simple pipeline as follows:

1. A page is added to the `checkLinks` channel. A `checkLinks` worker checks if the page connects to the end page. If it does, a path has been found! If not, add the page to the `getLinks` channel.
2. A `getLinks` worker takes the page and adds all the pages linked to from the page (children pages in a sense) to the `checkLinks` channel.

These stages are describes in more detail below:

#### checkLinks

The `checkLinks` channel contains pages which _may_ link to the end page. `checkLinks` workers take up to 50 pages from the `checkLinks` channel and checks if any of them link to the end. The workers call the MediaWiki API with several parameters including `prop=links` and `pltitles=<end title>` parameters. The `pltitles` parameter is **extremely** powerful: it checks whether any of up to 50 pages links to a certain page (but still returns a response in a few hundred milliseconds!)

If none of the pages link to the end, the pages are added to the `getLinks` channel.

#### getLinks

The `getLinks` channel contains pages which _do not_ link to the end page. `getLinks` workers take a page from the `getLinks` channel and adds all the pages linked from that page to the `checkLinks` channel. These workers also called the MediaWiki API with `prop=links`. To make sure pages from all parts of the alphabet are explored, `pldir` is randomly switched between `descending` and `ascending`.

#### More details

There are many ways to parse JSON in Go, but I opted to use the `buger/jsonparser` library for a few reasons:

- It doesn't require you to recreated the structure of the JSON in a `struct` beforehand. This makes programming much faster.
- In benchmarks, `jsonparser` is as fast or faster than all other Go JSON parsing libraries. [See here](https://github.com/buger/jsonparser#benchmarks). It is 10x faster than the standard `encoding/json` package!

## Strategies attempted

I tried

## Time spent on project

- Core implementation: 10 hours.
- Testing and documentation: 2 hours.
