package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/didip/tollbooth"
	"github.com/didip/tollbooth_chi"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/render"
	log "github.com/go-pkgz/lgr"
	"github.com/go-pkgz/rest"
	"github.com/go-pkgz/rest/logger"
	"github.com/pkg/errors"
)

// Server is json-rpc server with an optional basic auth
type Server struct {
	API        string
	AuthUser   string
	AuthPasswd string
	Version    string
	AppName    string
	Logger     L
	funcs      struct {
		m    map[string]ServerFn
		once sync.Once
	}

	httpServer struct {
		*http.Server
		sync.Mutex
	}
}

// Encoder is a function to encode call's result to Response
type Encoder func(id uint64, resp interface{}, e error) (Response, error)

// ServerFn handler registered for each method with Add
// Implementations provided by consumer and define response logic.
type ServerFn func(id uint64, params json.RawMessage) Response

// Run http server on given port
func (s *Server) Run(port int) error {
	if s.AuthUser == "" || s.AuthPasswd == "" {
		log.Print("[WARN] extension server runs without auth")
	}
	if s.funcs.m == nil && len(s.funcs.m) == 0 {
		return errors.Errorf("nothing mapped for dispatch, Add has to be called prior to Run")
	}

	router := chi.NewRouter()
	router.Use(middleware.Throttle(1000), middleware.RealIP, rest.Recoverer(log.Default()))
	router.Use(rest.AppInfo(s.AppName, "umputun", s.Version), rest.Ping)
	logInfoWithBody := logger.New(logger.Log(s.Logger), logger.WithBody, logger.Prefix("[INFO]")).Handler
	router.Use(middleware.Timeout(5 * time.Second))
	router.Use(logInfoWithBody, tollbooth_chi.LimitHandler(tollbooth.NewLimiter(1000, nil)), middleware.NoCache)
	router.Use(s.basicAuth)

	router.Post(s.API, s.handler)

	s.httpServer.Lock()
	s.httpServer.Server = &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	s.httpServer.Unlock()

	log.Printf("[INFO] listen on %d", port)
	return s.httpServer.ListenAndServe()
}

// EncodeResponse convert anything to Response
func (s *Server) EncodeResponse(id uint64, resp interface{}, e error) (Response, error) {
	v, err := json.Marshal(&resp)
	if err != nil {
		return Response{}, err
	}
	if e != nil {
		return Response{ID: id, Result: nil, Error: e.Error()}, nil
	}
	raw := json.RawMessage{}
	if err = raw.UnmarshalJSON(v); err != nil {
		return Response{}, err
	}
	return Response{ID: id, Result: &raw}, nil
}

// Shutdown http server
func (s *Server) Shutdown() error {
	s.httpServer.Lock()
	defer s.httpServer.Unlock()
	if s.httpServer.Server == nil {
		return errors.Errorf("http server is not running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// Add method handler
func (s *Server) Add(method string, fn ServerFn) {
	s.httpServer.Lock()
	defer s.httpServer.Unlock()
	if s.httpServer.Server != nil {
		log.Printf("[WARN] ignored method %s, can't be added to activated server", method)
		return
	}

	s.funcs.once.Do(func() {
		s.funcs.m = map[string]ServerFn{}
	})

	s.funcs.m[method] = fn
	log.Printf("[INFO] add handler for %s", method)
}

// HandlersGroup alias for map of handlers
type HandlersGroup map[string]ServerFn

// Group of handlers with common prefix
func (s *Server) Group(prefix string, m HandlersGroup) {
	for k, v := range m {
		s.Add(prefix+"."+k, v)
	}
}

func (s *Server) handler(w http.ResponseWriter, r *http.Request) {
	req := struct {
		ID     uint64           `json:"id"`
		Method string           `json:"method"`
		Params *json.RawMessage `json:"params"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		rest.SendErrorJSON(w, r, s.Logger, http.StatusBadRequest, err, req.Method)
		return
	}
	fn, ok := s.funcs.m[req.Method]
	if !ok {
		rest.SendErrorJSON(w, r, s.Logger, http.StatusNotImplemented, errors.New("unsupported method"), req.Method)
		return
	}

	params := json.RawMessage{}
	if req.Params != nil {
		params = *req.Params
	}

	render.JSON(w, r, fn(req.ID, params))
}

func (s *Server) basicAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if s.AuthUser == "" || s.AuthPasswd == "" {
			h.ServeHTTP(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if user != s.AuthUser || pass != s.AuthPasswd || !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// L defined logger interface used for an optional rest logging
type L interface {
	Logf(format string, args ...interface{})
}

// Func type is an adapter to allow the use of ordinary functions as Logger.
type Func func(format string, args ...interface{})

// Logf calls f(id)
func (f Func) Logf(format string, args ...interface{}) { f(format, args...) }

// NoOp logger
var NoOp = Func(func(format string, args ...interface{}) {})

// Std logger sends to std default logger directly
var Std = Func(func(format string, args ...interface{}) { log.Printf(format, args...) })