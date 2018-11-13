package graceful

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type Logger interface {
	Printf(format string, args ...interface{})
}

var logger Logger = log.New(os.Stderr, "", log.LstdFlags)

// HTTPGraceTime is http servers shutdown duration
var HTTPGraceTime = time.Second * 5

// SetLogger changes internal std log
func SetLogger(lg Logger) {
	logger = lg
}

// Stoppable is interface for GracefulStop objects that have GracefulStop
type Stoppable interface {
	GracefulStop()
}

type Servable interface {
	Stoppable
	Serve() error
}

// NetServable is for Grpc Server
type NetServable interface {
	Stoppable
	Serve(net.Listener) error
}

type serverWrapper struct {
	sc   NetServable
	addr string
}

func (sw *serverWrapper) Serve() error {
	network := "tcp"
	if u, err := url.Parse(sw.addr); err == nil {
		if u.Scheme == "unix" || u.Scheme == "unixs" {
			network = "unix"
			sw.addr = u.Host + u.Path
		}
	}
	logger.Printf("listen %s on %s", network, sw.addr)
	lis, err := net.Listen(network, sw.addr)
	if err != nil {
		return err
	}
	return sw.sc.Serve(lis)
}

func (sw *serverWrapper) GracefulStop() {
	sw.sc.GracefulStop()
}

type httpWrapper struct {
	s        *http.Server
	certFile string
	keyFile  string
	tls      bool
}

func (sw *httpWrapper) Serve() error {
	logger.Printf("serving HTTP on %s", sw.s.Addr)
	if sw.tls {
		return sw.s.ListenAndServeTLS(sw.certFile, sw.keyFile)
	}
	return sw.s.ListenAndServe()
}

func (sw *httpWrapper) GracefulStop() {
	ctx, cancel := context.WithTimeout(context.Background(), HTTPGraceTime)
	defer cancel()
	sw.s.Shutdown(ctx)
}

// NewServable creates wrapper for grpc servers in order to graceful stop
func NewServable(address string, ns NetServable) Servable {
	return &serverWrapper{sc: ns, addr: address}
}

// ListenAndServe creates wrapper for http.Server in order to graceful shutdown
func ListenAndServe(address string, handler http.Handler) Servable {
	return &httpWrapper{s: &http.Server{Addr: address, Handler: handler}}
}

// ListenAndServeTLS creates wrapper for http.Server in order to graceful shutdown
func ListenAndServeTLS(address string, certfile string, keyfile string, handler http.Handler) Servable {
	return &httpWrapper{
		s:        &http.Server{Addr: address, Handler: handler},
		certFile: certfile,
		keyFile:  keyfile,
		tls:      true,
	}
}

func stopOnSignalsChannel(sigs chan os.Signal, gss ...Stoppable) {
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigs
	logger.Printf("graceful: '%v' RECEIVED", sig)
	// Stop one by one
	for _, gs := range gss {
		if gs != nil {
			gs.GracefulStop()
		}
	}
}

// StopOnSignals listens SIGINT and SIGTERM to call given GracefulStops. It is blocks
func StopOnSignals(gss ...Stoppable) {
	sigs := make(chan os.Signal, 1)
	stopOnSignalsChannel(sigs, gss...)
}

// StopOnSignalsWithChan listens SIGINT and SIGTERM to call GracefulStop. After calling GracefulStop send channel message
func StopOnSignalsWithChan(c chan<- error, gss ...Stoppable) {
	StopOnSignals(gss...)
	c <- nil
}

// AsyncStopOnSignals listens SIGINT and SIGTERM to call GracefulStop. Returns a channel to wait for close
func AsyncStopOnSignals(gss ...Stoppable) <-chan error {
	c := make(chan error, 1)
	go StopOnSignalsWithChan(c, gss...)
	return c
}

// ServeAndStopOnSignals starts Servable objects and waits for signals
func ServeAndStopOnSignals(gss ...Stoppable) <-chan error {
	// Init channels, mutex and error
	c := make(chan error, 1)
	sigs := make(chan os.Signal, 1)
	var m = &sync.Mutex{}
	var errout error

	// Serve Servable objects
	for _, g := range gss {
		if s, ok := g.(Servable); ok {
			go func() {
				err := s.Serve()
				logger.Printf("ServeAndStopOnSignals: Serving stopped, err=%v", err)
				if err != nil {
					select {
					case sigs <- syscall.SIGABRT:
					default:
					}
					m.Lock()
					errout = err
					m.Unlock()
				}
			}()
		}
	}
	// Wait for signals on seperate goroutine
	go func() {
		stopOnSignalsChannel(sigs, gss...)
		// Check for error.
		var e error
		m.Lock()
		e = errout
		m.Unlock()
		c <- e
	}()
	return c
}
