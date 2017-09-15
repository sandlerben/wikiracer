wikiracer
=========

wikiracer is a Go application which plays ["The Wikipedia Game"](https://en.wikipedia.org/wiki/Wikiracing). It takes a start page and an end page and follows hyperlinks to get from the start page to the end page as quickly as possible.

Table of Contents
=================

   * [Basic Usage](#basic-usage)
      * [Customizing behavior](#customizing-behavior)
   * [Installation](#installation)
   * [Run tests](#run-tests)
   * [Profiling](#profiling)
   * [Why Go?](#why-go)
   * [Architecture overview](#architecture-overview)
      * [Web](#web)
      * [Race](#race)
         * [checkLinks](#checklinks)
         * [getLinks](#getlinks)
         * [More details](#more-details)
   * [Some strategies attempted](#some-strategies-attempted)
   * [Time spent on project](#time-spent-on-project)
   * [Appendix](#appendix)

# Basic Usage

Start a wikiracer server on port `8000`.

```
$ wikiracer
INFO[0000] Server is running at http://localhost:8000
```

In order to initiate a race, make a `GET` request to the server's `/race` endpoint with the following arguments.

- starttitle **(required)**: The Wikipedia page to start from.
- endtitle **(required)**: The Wikipedia page to find a path to.
- nocache: By default, the server caches all paths previously found. To ignore the cache for this race, set `nocache=1`.

The endpoint returns a JSON response containing a path from the start page to the end page and how long it took to find the path.

```json
{
    "path": [
        "English language",
        "American English",
        "University of Pennsylvania"
    ],
    "time_taken": "384.565858ms"
}
```

If no path was found in the time limit (see below), the JSON response will look like:

```json
{
    "message": "no path found within 1m0s",
    "path": [],
    "time_taken": "1m0s"
}
```

## Customizing behavior

The following environment variables can be used to customize the behavior of wikiracer.

- `WIKIRACER_PORT`: The port on which to run a HTTP server (default `8000`).
- `NUM_CHECK_LINKS_ROUTINES`: The number of concurrent checkLinks workers to run (default 10).
- `NUM_GET_LINKS_ROUTINES`: The number of concurrent getLinks workers to run (default 5).
- `EXPLORE_ALL_LINKS`: Sometimes, the MediaWiki API doesn't return all links in once response. As a result, wikiracer continues to query the MediaWiki API until all the links are returned. If `EXPLORE_ALL_LINKS` is set to `"false"`, then wikiracer will not continue even if there are more links.
- `WIKIRACER_TIME_LIMIT`: The time limit for the race, after which wikiracer gives up. Must be a string which can be understood by [time.ParseDuration](https://golang.org/pkg/time/#ParseDuration).

# Installation

Create a directory and clone the repo:

```
$ mkdir -p $GOPATH/src/github.com/sandlerben/wikiracer
$ cd $GOPATH/src/github.com/sandlerben/wikiracer
$ git clone https://github.com/sandlerben/wikiracer.git .
```

Install the dependencies using [dep](https://github.com/golang/dep), the semi-official Go dependency management tool.

```
$ dep ensure
```

Finally, install wikiracer.

```
$ go install
```

# Run tests

The full test suite can be run with:

```
$ go test ./...
```

# Profiling

wikiracer exposes a [pprof endpoint](https://blog.golang.org/profiling-go-programs) which allows it to be profiled in a few ways:

- Using [go-torch](https://github.com/uber/go-torch), which can generate a [flamegraph](https://github.com/uber/go-torch#example-flame-graph) visualizing the program's workload. (Side note: I wrote this tool!)
- Using `go tool pprof`, which can create CPU profiles, memory profiles, and blocking profiles and visualize each in various different ways.

# Why Go?

I wrote this application in Go for a few reasons:

- I have experience using Go in the past from [go-torch](https://github.com/uber/go-torch) and [transcribe4all](https://github.com/hack4impact/transcribe4all).
- Go has extremely powerful concurrency primitives ([goroutines](https://gobyexample.com/goroutines) and [channels](https://tour.golang.org/concurrency/2)) and an excellent runtime/scheduler which make writing fast, thread-safe concurrent programs more straightforward than other languages.
- Go has a great standard library. In particular, it's easy to create a full-featured HTTP server and make HTTP requests using Go's built-ins.

# Architecture overview

[![wikiracer overview](./wikiracer_overview.svg)](./wikiracer_overview.svg)

## Web

The `wikiracer/web` package encapsulates the logic for handling HTTP requests. The package exposes two endpoints:

- `/race` returns a path from a start page to an end page.
- `/health` returns a message indicating that the server is alive and healthy.

The `wikiracer/web` package uses the `gorilla/mux` router, an extremely popular Go URL dispatcher.

The `wikiracer/web` package also features a simple cache of paths previously found (implemented as a Go map). That way, a path from same start to some end page only needs to be found once.

## Race

The `wikiracer/race` package encapsulates the Wikipedia exploring logic; it is the most interesting part of the application.

A `race.Racer` encapsulates all the state needed for one race, including:

- The page to start at
- The page to find a path to
- A record of how each page was reached during the race
- A record of the links explored so far
- Synchronization primitives (`sync.Once`)
- Channels to allow concurrent workers to communicate  

A `race.Racer` exposes one public function, `Run`, which returns a path from a start page to an end page.

Under the hood, there is a lot going on inside the `race` package. Specifically, a number of `checkLinks` workers and `getLinks` workers concurrently explore the graph of Wikipedia pages while communicating with each other (via channels) until a path to the end page is found.

At a high level, pages pass through the following pipeline:

1. A page is added to the `checkLinks` channel. A `checkLinks` worker checks if the page connects to the end page. If it does, a path has been found! If not, add the page to the `getLinks` channel.
2. A `getLinks` worker takes the page and adds all the pages linked to from the page ("children" pages) to the `checkLinks` channel.

These stages are described in more detail below.

### checkLinks

The `checkLinks` channel contains pages which _may_ link to the end page. `checkLinks` workers take up to 50 pages from the `checkLinks` channel and checks if any of them link to the end page. The workers call the MediaWiki API with several parameters including `prop=links` and `pltitles=<end title>` parameters. The `pltitles` parameter is **extremely** useful: it asks the MediaWiki API to check whether any of up to 50 pages links to a certain page. It returns a response in only a few hundred milliseconds!

If none of the pages link to the end, the pages are written to the `getLinks` channel.

### getLinks

The `getLinks` channel contains pages which _do not_ link to the end page. `getLinks` workers take a page from the `getLinks` channel and adds all the pages linked from that page to the `checkLinks` channel. These workers also called the MediaWiki API with `prop=links`. To make sure pages pages from all parts of the alphabet are explored, `pldir` is randomly switched between `descending` and `ascending`.

### More details

Lots more fun technical implementation details can be found in the [appendix below](#appendix).

# Some strategies attempted

### No pipeline: getLinks workers can write directly to the getLinks channel, checkLinks workers can write directly to the checkLinks channel

In my final implementation, pages pass through a two-stage pipeline: first they are handled by `checkLinks` and then they are handled by `getLinks`.

I tried making the stages less rigid by allowing getLinks workers to immediately pass pages to other getLinks workers (instead of having to pass them to checkLinks workers). This approach noticeably decreased the performance of wikiracer because pages which actually linked to the end page were traversed further and further unnecessarily.

### Different number of worker goroutines and how to return immediately when a path is found

As mentioned above, the number of `checkLinks`/`getLinks` worker goroutine can be customized. However, I wanted to find the best default for most races.

First, some background. All workers check to see if a channel called `done` is closed before getting work from the `getLinks` or `checkLinks` channels. A closing of `done` is the signal that all the workers should stop working and exit.

In the original implementation, the main request goroutine waited for **all** worker goroutines to exit using a [`sync.WaitGroup`](https://golang.org/pkg/sync/#WaitGroup). When the end page was found, the following happened:

1. A `checkLinks` worker found the end page. Success!
2. The worker closed `done` to signal that all the other workers could stop.
3. [Eventually, maybe after a second or more] All the workers reached the code which checks if `done` is closed and then exited.
4. After all workers exited, the main goroutine was unblocked and returned the path.

A key problem with this approach was that **the time taken by step 3 grew with the number of concurrent workers**.

I started with two `checkLinks` workers and two `getLinks` workers. While increasing the number of concurrent workers led to the end page being found faster, these gains were eaten up by **a longer step 3**.

I solved this problem by having the main goroutine wait for the `done` channel to be closed instead of using a `sync.WaitGroup`. In effect, this unblocks the main goroutine even before all the worker goroutines exit. This approach, coupled with increasing the number of concurrent workers significantly increased the response time (doubling/tripling it in some cases!).

### Not exploring all links on a page

Sometimes, the MediaWiki API doesn't return all links for a page in once response. The default behavior of the application is to query the API until all the links are returned. However, I hypothesized that _not_ exploring all the links for a page would make wikiracer faster. My thoughts were the following:

- Links on a Wikipedia page are probably pretty similar (topic-wise) to that page.
- If all the links on every page were explored, the wikiracer could get "trapped" in one topic area of Wikipedia and keep exploring related pages.
- Therefore, ignoring some links on pages would get to other parts of Wikipedia and find the end page faster.

It turned out that when I tested this approach, wikiracer did consistently worse, even when the start page and end page were totally unrelated. (See for yourself by setting `EXPLORE_ALL_LINKS` to `false`.) This was the case for a few reasons.

- Sometimes the best way to get out of one part of Wikipedia is to get to a very general and link-abundant page (e.g. ["United States"](https://en.wikipedia.org/wiki/United_States)). Without exploring all links, these general pages would often be skipped.
- Not only that, but upon arriving on a general page with lots of links, only a small fraction of those links would be explored.
- Finally, sometimes there is really only one way to get from one page to another. For example, the best way to get from ["USB-C"](https://en.wikipedia.org/wiki/USB-C) to ["Weston, Florida"](https://en.wikipedia.org/wiki/Weston,_Florida) is to pass through ["American Express"](https://en.wikipedia.org/wiki/American_Express) (any other path has many more hops). Without exploring all links, a "key" page like "American Express" can be easily missed.

# Time spent on project

- Core implementation: 15 hours.
  - Web: 1 hour.
  - Race: 14 hours.
- Testing and documentation: 3 hours.

# Appendix

## More technical details

### JSON parsing

There are many ways to parse JSON in Go, but I opted to use the `buger/jsonparser` library for a few reasons:

- It doesn't require you to recreate the structure of the JSON in a `struct` beforehand. This makes programming much faster.
- In benchmarks, `jsonparser` is as fast or faster than all other Go JSON parsing libraries. [See here](https://github.com/buger/jsonparser#benchmarks). It is 10x faster than the standard `encoding/json` package!

### Handling "Too Many Requests"

Clearly, the MediaWiki API ought to be called as often as possible in order to find a path as fast as possible. However, the API documentation [does not include a clear quota or request limit](https://www.mediawiki.org/wiki/API:Etiquette#Request_limit). Rather, the API will return `429 Too Many Requests` occasionally.

To get around this, I abstracted the core requesting code into a function called `loopUntilResponse` which makes a request. If it receives a `429 Too Many Requests`, it waits for 100 milliseconds and tries the request again.

### Time limit

The time limit is enforced by the `giveUpAfterTime` worker. It takes a `time.Timer`, and when the `Timer` finishes, the `giveUpAfterTime` reads a message from the `timer.C` channel and closes the `done` channel.

### Mocking

[Mock testing](https://github.com/stretchr/testify) is key to isolating a specific part of the code in a unit test. Therefore, when testing the `race` package, I used [`httpmock`](https://github.com/jarcoal/httpmock) to mock the responses to `http.Get`. When testing the `web` package, I used [`mockery`](https://github.com/vektra/mockery) and [`testify`](https://github.com/stretchr/testify) to create a mock `race.Racer` for testing.
