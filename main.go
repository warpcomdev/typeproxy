package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	TYPEPROXY_PORT      = 8888 // Port to listen on
	TYPEPROXY_PORT_MIN  = 1024
	TYPEPROXY_PORT_MAX  = 65535
	TYPEPROXY_GRACE     = 10 // Grace interval for shutdown, seconds
	TYPEPROXY_GRACE_MIN = 5
	TYPEPROXY_GRACE_MAX = 600
	TYPEPROXY_ENV_URL   = "TYPEPROXY_URL"   // Env variable to read for proxy URL
	TYPEPROXY_ENV_PORT  = "TYPEPROXY_PORT"  // Env variable to read for proxy port
	TYPEPROXY_ENV_GRACE = "TYPEPROXY_GRACE" // Env variable to read for Grace period
)

// newProxy creates reverse proxy that overrides Content-Type on POST
func newProxy(target *url.URL, timeout, keepalive time.Duration) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(target)
	oldDirector := p.Director
	p.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   timeout,
			KeepAlive: keepalive,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       keepalive,
		TLSHandshakeTimeout:   timeout,
		ExpectContinueTimeout: time.Second,
	}
	p.Director = func(r *http.Request) {
		oldDirector(r)
		// Change POST body content-type to application/json
		if r.Method == http.MethodPost {
			ct := strings.Join(r.Header.Values("Content-Type"), ", ")
			log.Println(r.Proto, r.Method, "Content-Type:", ct, r.URL.String())
			if r.Body != nil {
				// Read the whole body
				data, err := io.ReadAll(r.Body)
				if !errors.Is(err, io.EOF) {
					return
				}
				r.Body.Close()
				// If we can decode it as json, add a "contentType" field
				var content map[string]interface{}
				dec := json.NewDecoder(bytes.NewReader(data))
				if err := dec.Decode(&content); err == nil {
					r.Header.Set("Content-Type", "application/json")
					content["contentType"] = ct
					if data, err = json.Marshal(content); err != nil {
						r.Header.Del("Content-Length") // just in case
					}
				}
				// Replace the body with whatever we could do
				r.Body = ioutil.NopCloser(bytes.NewReader(data))
			}
		} else {
			log.Println(r.Proto, r.Method, r.URL.String())
		}
	}
	return p
}

// config struct holds configurable params for program
type config struct {
	URL   *url.URL // URL to forward traffic to
	Port  int      // Port to listen on
	Grace int      // Grace interval duration (seconds)
}

// newConfig reads config from args or env
func newConfig() (config, error) {
	var c config
	defPort, err := envInt(TYPEPROXY_ENV_PORT, TYPEPROXY_PORT)
	if err != nil {
		return c, err
	}
	defGrace, err := envInt(TYPEPROXY_ENV_GRACE, TYPEPROXY_GRACE)
	if err != nil {
		return c, err
	}
	flag.IntVar(&c.Port, "port", defPort, fmt.Sprintf("TCP Port to listen to (Env %s)", TYPEPROXY_ENV_PORT))
	flag.IntVar(&c.Grace, "grace", defGrace, fmt.Sprintf("Grace interval for shutdown (seconds) (Env %s)", TYPEPROXY_ENV_GRACE))
	flag.Parse()
	if c.Port < TYPEPROXY_PORT_MIN || c.Port > TYPEPROXY_PORT_MAX {
		return c, fmt.Errorf("Invalid port number %d, must be between %d and %d", c.Port, TYPEPROXY_PORT_MIN, TYPEPROXY_PORT_MAX)
	}
	if c.Grace < TYPEPROXY_GRACE_MIN || c.Grace > TYPEPROXY_GRACE_MAX {
		return c, fmt.Errorf("Invalid grace interval %d, must be between %d and %d seconds", c.Grace, TYPEPROXY_GRACE_MIN, TYPEPROXY_GRACE_MAX)
	}
	var urlString string
	if flag.NArg() > 0 {
		urlString = flag.Arg(0)
	} else {
		urlString = envString(TYPEPROXY_ENV_URL, "")
	}
	if urlString == "" {
		return c, fmt.Errorf("Missing URL command line parameter or Env variable %s", TYPEPROXY_ENV_URL)
	}
	if c.URL, err = url.Parse(urlString); err != nil {
		return c, err
	}
	return c, nil
}

// Main keeps forwarding traffic
func main() {

	config, err := newConfig()
	if err != nil {
		flag.Usage()
		log.Fatal(err.Error())
	}
	// Timeout and keepalive are derived from grace period interval
	timeout := time.Duration(config.Grace) * time.Second / 2
	keepalive := time.Duration(config.Grace) * time.Second * 3
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.Port),
		Handler: newProxy(config.URL, timeout, keepalive),
	}

	sigs := make(chan os.Signal, 1)
	done := make(chan struct{})
	wait := sync.WaitGroup{}
	wait.Add(1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		defer wait.Done()
		select {
		case <-sigs:
			break
		case <-done:
			break
		}
		log.Println("Cancelling server, waiting up to", config.Grace, "seconds")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(config.Grace))
		defer cancel()
		_ = srv.Shutdown(ctx) // ignore shutdown error
	}()

	log.Println("Forwarding requests on port", config.Port, "to", config.URL.String())
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err.Error())
	}
	close(done)
	wait.Wait()
}

// envString reads string from environment
func envString(flagName string, defaultValue string) string {
	if val, ok := os.LookupEnv(flagName); ok {
		return val
	}
	return defaultValue
}

// envInt reads int from environment
func envInt(flagName string, defaultValue int) (int, error) {
	if val, ok := os.LookupEnv(flagName); ok {
		return strconv.Atoi(val)
	}
	return defaultValue, nil
}
