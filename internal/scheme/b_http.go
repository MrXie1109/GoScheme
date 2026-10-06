// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The (goscheme http) library.  Both halves are deliberately small: a request
// returns the body as a string, which is what a script usually wants, and the
// full response is available when it is not.  A server is a handler procedure
// and a port, with one interpreter thread per request — the same shape as
// (go ...) per connection, since that is exactly what net/http does.

// HTTPRequest is a request handed to a handler.
type HTTPRequest struct {
	method  string
	url     string
	path    string
	query   string
	headers map[string]string // lower-cased names
	body    string
}

// SchemeDescribe prints the request; see re.Describer.
func (r *HTTPRequest) SchemeDescribe(Printer) string {
	return fmt.Sprintf("#<http-request %s %s>", r.method, r.path)
}

// HTTPResponse is what a handler returns.
type HTTPResponse struct {
	status      int64
	body        string
	contentType string
	headers     map[string]string
}

// SchemeDescribe prints the response; see re.Describer.
func (r *HTTPResponse) SchemeDescribe(Printer) string {
	return fmt.Sprintf("#<http-response %d, %d bytes>", r.status, len(r.body))
}

// HTTPServer is a running server.
type HTTPServer struct {
	mu       sync.Mutex
	srv      *http.Server
	ln       net.Listener
	closed   bool
	requests int64
}

// SchemeDescribe prints the server; see re.Describer.
func (s *HTTPServer) SchemeDescribe(Printer) string {
	return fmt.Sprintf("#<http-server %s>", s.ln.Addr().String())
}

const httpClientTimeout = 30 * time.Second

func installHTTP(m *Machine) {
	const lib = "(goscheme http)"

	// ------------------------------------------------------------- requests
	// (http-get url [headers]) returns the body.  headers is an alist of
	// strings, which is enough for the usual "authorization" or "accept" case.
	// The methods that can carry a body take (url [body [headers]]); a body is
	// recognised by being a string, so (http-delete url headers) and
	// (http-delete url body headers) both mean what they look like.
	for _, method := range []string{"GET", "POST", "PUT", "DELETE", "HEAD"} {
		name := "http-" + strings.ToLower(method)
		sendsBody := method == "POST" || method == "PUT" || method == "DELETE"
		maxArgs := 2
		if sendsBody {
			maxArgs = 3
		}
		m.def(name, 1, maxArgs, func(m *Machine, a []Value) {
			url := wantString(name, a[0]).Value()
			body := ""
			headerIndex := 1
			if sendsBody && len(a) > 1 {
				if s, ok := a[1].(*String); ok {
					body = s.Value()
					headerIndex = 2
				}
			}
			req, err := http.NewRequest(method, url, strings.NewReader(body))
			if err != nil {
				m.Raise(NewError(name + ": " + err.Error()))
				return
			}
			if body != "" {
				req.Header.Set("Content-Type", "text/plain; charset=utf-8")
			}
			if len(a) > headerIndex {
				applyHeaderAlist(name, req, a[headerIndex])
			}
			resp, err := httpClient().Do(req)
			if err != nil {
				m.Raise(NewFileError(name+": "+err.Error(), NewString(url)))
				return
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				m.Raise(NewError(name + ": " + err.Error()))
				return
			}
			m.Return(NewString(string(data)))
		}, lib)
	}

	// (http-request method url [body [headers]]) returns the whole response, so
	// that a status code or a header can be looked at.
	m.def("http-request", 2, 4, func(m *Machine, a []Value) {
		method := wantString("http-request", a[0]).Value()
		url := wantString("http-request", a[1]).Value()
		body := ""
		if len(a) > 2 {
			if s, ok := a[2].(*String); ok {
				body = s.Value()
			}
		}
		req, err := http.NewRequest(strings.ToUpper(method), url, strings.NewReader(body))
		if err != nil {
			m.Raise(NewError("http-request: " + err.Error()))
			return
		}
		if len(a) > 3 {
			applyHeaderAlist("http-request", req, a[3])
		}
		resp, err := httpClient().Do(req)
		if err != nil {
			m.Raise(NewFileError("http-request: "+err.Error(), NewString(url)))
			return
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			m.Raise(NewError("http-request: " + err.Error()))
			return
		}
		m.Return(&HTTPResponse{
			status:      int64(resp.StatusCode),
			body:        string(data),
			contentType: resp.Header.Get("Content-Type"),
			headers:     flattenHeader(resp.Header),
		})
	}, lib)

	m.defSimple("http-response?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*HTTPResponse)
		return BooleanOf(ok), nil
	}, lib)

	m.defSimple("http-response-status", 1, 1, func(a []Value) (Value, error) {
		return Int(wantHTTPResponse("http-response-status", a[0]).status), nil
	}, lib)

	m.defSimple("http-response-body", 1, 1, func(a []Value) (Value, error) {
		return NewString(wantHTTPResponse("http-response-body", a[0]).body), nil
	}, lib)

	m.defSimple("http-response-content-type", 1, 1, func(a []Value) (Value, error) {
		return NewString(wantHTTPResponse("http-response-content-type", a[0]).contentType), nil
	}, lib)

	// Header names are matched case-insensitively, as HTTP requires.
	m.defSimple("http-response-header", 2, 2, func(a []Value) (Value, error) {
		r := wantHTTPResponse("http-response-header", a[0])
		name := strings.ToLower(wantString("http-response-header", a[1]).Value())
		if v, ok := r.headers[name]; ok {
			return NewString(v), nil
		}
		return False, nil
	}, lib)

	// --------------------------------------------------------------- server
	// (http-serve port handler [host]) starts a server and returns it.  The
	// handler is called with a request and returns either a string, which is
	// sent as a 200 text/plain response, or a response made by
	// http-response.  Port 0 asks the system for a free port.
	m.def("http-serve", 2, 3, func(m *Machine, a []Value) {
		port := wantIndex("http-serve", a[0])
		handler := a[1]
		host := "127.0.0.1"
		if len(a) == 3 {
			host = wantString("http-serve", a[2]).Value()
		}
		if port > 65535 {
			m.Raise(NewError("http-serve: port out of range: " + WriteToString(a[0])))
			return
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			m.Raise(NewError("http-serve: " + err.Error()))
			return
		}
		server := &HTTPServer{ln: ln}
		// Each request gets its own interpreter thread, exactly as (go ...)
		// would, and the machine it runs on is the one that installed us.
		server.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveRequest(m, handler, w, r, server)
		})}
		go func() {
			// Serve returns ErrServerClosed when the server is closed, which is
			// the normal way for this goroutine to end.
			_ = server.srv.Serve(ln)
		}()
		m.Return(server)
	}, lib)

	m.defSimple("http-server?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*HTTPServer)
		return BooleanOf(ok), nil
	}, lib)

	m.defSimple("http-server-port", 1, 1, func(a []Value) (Value, error) {
		s := wantHTTPServer("http-server-port", a[0])
		if addr, ok := s.ln.Addr().(*net.TCPAddr); ok {
			return Int(int64(addr.Port)), nil
		}
		return False, nil
	}, lib)

	m.defSimple("http-server-address", 1, 1, func(a []Value) (Value, error) {
		s := wantHTTPServer("http-server-address", a[0])
		return NewString(s.ln.Addr().String()), nil
	}, lib)

	m.defSimple("http-server-requests", 1, 1, func(a []Value) (Value, error) {
		s := wantHTTPServer("http-server-requests", a[0])
		s.mu.Lock()
		defer s.mu.Unlock()
		return Int(s.requests), nil
	}, lib)

	m.defSimple("http-server-close", 1, 1, func(a []Value) (Value, error) {
		s := wantHTTPServer("http-server-close", a[0])
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.closed {
			s.closed = true
			s.srv.Close()
		}
		return UnspecifiedValue, nil
	}, lib)

	// (http-server-wait server) blocks until the server is closed, which is what
	// keeps a script that only serves alive.
	m.defSimple("http-server-wait", 1, 1, func(a []Value) (Value, error) {
		s := wantHTTPServer("http-server-wait", a[0])
		for {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return UnspecifiedValue, nil
			}
			time.Sleep(20 * time.Millisecond)
		}
	}, lib)

	// (http-response status body [content-type]) builds what a handler returns
	// when a string is not enough.
	m.defSimple("http-response", 2, 3, func(a []Value) (Value, error) {
		status := wantExactInt64("http-response", a[0])
		body := wantString("http-response", a[1]).Value()
		contentType := "text/plain; charset=utf-8"
		if len(a) == 3 {
			contentType = wantString("http-response", a[2]).Value()
		}
		return &HTTPResponse{status: status, body: body, contentType: contentType}, nil
	}, lib)

	m.defSimple("http-request?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*HTTPRequest)
		return BooleanOf(ok), nil
	}, lib)

	m.defSimple("http-request-method", 1, 1, func(a []Value) (Value, error) {
		return NewString(wantHTTPRequest("http-request-method", a[0]).method), nil
	}, lib)

	m.defSimple("http-request-path", 1, 1, func(a []Value) (Value, error) {
		return NewString(wantHTTPRequest("http-request-path", a[0]).path), nil
	}, lib)

	m.defSimple("http-request-query", 1, 1, func(a []Value) (Value, error) {
		return NewString(wantHTTPRequest("http-request-query", a[0]).query), nil
	}, lib)

	m.defSimple("http-request-url", 1, 1, func(a []Value) (Value, error) {
		return NewString(wantHTTPRequest("http-request-url", a[0]).url), nil
	}, lib)

	m.defSimple("http-request-body", 1, 1, func(a []Value) (Value, error) {
		return NewString(wantHTTPRequest("http-request-body", a[0]).body), nil
	}, lib)

	m.defSimple("http-request-header", 2, 2, func(a []Value) (Value, error) {
		r := wantHTTPRequest("http-request-header", a[0])
		name := strings.ToLower(wantString("http-request-header", a[1]).Value())
		if v, ok := r.headers[name]; ok {
			return NewString(v), nil
		}
		return False, nil
	}, lib)
}

// serveRequest runs one request on its own interpreter thread.
func serveRequest(m *Machine, handler Value, w http.ResponseWriter, r *http.Request, server *HTTPServer) {
	// net/http runs this on its own goroutine, so it counts as an interpreter
	// thread for as long as it can touch environments.
	enterConcurrency()
	defer exitConcurrency()
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	r.Body.Close()

	request := &HTTPRequest{
		method:  r.Method,
		url:     r.URL.String(),
		path:    r.URL.Path,
		query:   r.URL.RawQuery,
		headers: flattenHeader(r.Header),
		body:    string(body),
	}

	// A panic in a handler must become a 500, not a dead process.
	defer func() {
		if rec := recover(); rec != nil {
			http.Error(w, "handler failed: "+describeThrown(rec), http.StatusInternalServerError)
		}
	}()

	child := m.Child()
	result, err := child.RunApply(handler, []Value{request}, child.Global)
	if err != nil {
		http.Error(w, "handler failed: "+err.Error(), http.StatusInternalServerError)
		server.count()
		return
	}

	switch v := result.(type) {
	case *HTTPResponse:
		if v.contentType != "" {
			w.Header().Set("Content-Type", v.contentType)
		}
		for k, val := range v.headers {
			w.Header().Set(k, val)
		}
		w.WriteHeader(int(v.status))
		io.WriteString(w, v.body)
	case *String:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, v.Value())
	default:
		http.Error(w, "handler returned "+WriteToString(result)+
			", which is neither a string nor an http-response", http.StatusInternalServerError)
	}
	server.count()
}

func (s *HTTPServer) count() {
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()
}

// describeThrown renders whatever a handler threw for an error page.
func describeThrown(r interface{}) string {
	switch e := r.(type) {
	case error:
		return e.Error()
	case Value:
		return WriteToString(e)
	}
	return "unknown error"
}

// httpClient is built per call so that no state is shared between threads.
func httpClient() *http.Client {
	return &http.Client{Timeout: httpClientTimeout}
}

// applyHeaderAlist copies an alist of ("name" . "value") onto a request.
func applyHeaderAlist(name string, req *http.Request, alist Value) {
	items, ok := ListToSlice(alist)
	if !ok {
		panic(errf(name, "expected an alist of header name and value but got %s",
			WriteToString(alist)))
	}
	for _, item := range items {
		p, ok := item.(*Pair)
		if !ok {
			panic(errf(name, "expected a pair in the header alist but got %s",
				WriteToString(item)))
		}
		key, ok := p.Car.(*String)
		if !ok {
			panic(errf(name, "expected a string header name but got %s", WriteToString(p.Car)))
		}
		val, ok := p.Cdr.(*String)
		if !ok {
			panic(errf(name, "expected a string header value but got %s", WriteToString(p.Cdr)))
		}
		req.Header.Set(key.Value(), val.Value())
	}
}

// flattenHeader turns http.Header into a map with lower-cased names, joined
// with commas the way a single header line would be.
func flattenHeader(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return out
}

func wantHTTPRequest(name string, v Value) *HTTPRequest {
	r, ok := v.(*HTTPRequest)
	if !ok {
		panic(errf(name, "expected an http request but got %s", WriteToString(v)))
	}
	return r
}

func wantHTTPResponse(name string, v Value) *HTTPResponse {
	r, ok := v.(*HTTPResponse)
	if !ok {
		panic(errf(name, "expected an http response but got %s", WriteToString(v)))
	}
	return r
}

func wantHTTPServer(name string, v Value) *HTTPServer {
	s, ok := v.(*HTTPServer)
	if !ok {
		panic(errf(name, "expected an http server but got %s", WriteToString(v)))
	}
	return s
}

func init() { registerInstaller(installHTTP) }
