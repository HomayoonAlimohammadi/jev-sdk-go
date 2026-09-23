package jev

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// countingServer counts the TCP connections opened to it, and can hold a
// burst of requests until all of them have arrived, which forces each request
// in the burst onto a connection of its own.
type countingServer struct {
	*httptest.Server

	opened atomic.Int32

	mu      sync.Mutex
	barrier *burstBarrier
}

type burstBarrier struct {
	size    int32
	arrived atomic.Int32
	release chan struct{}
}

func newCountingServer(t *testing.T) *countingServer {
	t.Helper()

	s := &countingServer{}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)

		s.mu.Lock()
		barrier := s.barrier
		s.mu.Unlock()
		if barrier != nil {
			if barrier.arrived.Add(1) == barrier.size {
				close(barrier.release)
			}
			<-barrier.release
		}

		respondJSON(http.StatusOK, `{"models":[]}`)(w, r)
	}))
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			s.opened.Add(1)
		}
	}
	s.Start()
	t.Cleanup(s.Close)
	return s
}

// burst sends size concurrent calls, held at the server until all are in
// flight, and reports how many new connections the burst had to open.
func (s *countingServer) burst(t *testing.T, client *Client, size int) int {
	t.Helper()

	s.mu.Lock()
	s.barrier = &burstBarrier{size: int32(size), release: make(chan struct{})}
	s.mu.Unlock()

	before := s.opened.Load()

	var wg sync.WaitGroup
	for range size {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.ListModels(t.Context()); err != nil {
				t.Errorf("ListModels() error = %v", err)
			}
		}()
	}
	wg.Wait()

	return int(s.opened.Load() - before)
}

func TestDefaultClientKeepsConnectionsForConcurrentUse(t *testing.T) {
	t.Parallel()

	// A client is meant to be shared by concurrent callers of one host. Once
	// a burst has opened its connections, the next burst of the same size
	// must reuse them rather than close and redial all but a couple.
	const concurrency = 8

	server := newCountingServer(t)
	client, err := New(WithAPIKey("k"), WithBaseURL(server.URL), WithRetry(RetryPolicy{}))
	if err != nil {
		t.Fatal(err)
	}

	if opened := server.burst(t, client, concurrency); opened != concurrency {
		t.Fatalf("the first burst opened %d connections, want %d: the test needs each request on its own", opened, concurrency)
	}

	// A small allowance absorbs a connection that has not yet been returned to
	// the pool when the next burst starts; a pool of two would redial six.
	if opened := server.burst(t, client, concurrency); opened > 2 {
		t.Errorf("the second burst opened %d new connections, want the first burst's reused", opened)
	}
}

// This test must not run in parallel. It watches http.DefaultTransport's idle
// pool, which every httptest.Server clears when it closes, so a parallel test
// finishing mid-way would evict the connection and fail it spuriously.
func TestCloseIdleConnectionsLeavesOtherClientsAlone(t *testing.T) {
	// A connection pooled by other code in the process, on the shared default
	// transport, must survive the SDK client closing its own idle connections.
	server := newCountingServer(t)

	get := func() {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+modelsPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	get() // pools a connection on http.DefaultTransport
	before := server.opened.Load()

	client, err := New(WithAPIKey("k"), WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	client.CloseIdleConnections()

	get()
	if opened := server.opened.Load() - before; opened != 0 {
		t.Errorf("another client had to redial %d connections after the SDK closed its own idle ones", opened)
	}
}

// wrappingTransport stands in for process-wide instrumentation that replaces
// http.DefaultTransport with a wrapper around it.
type wrappingTransport struct {
	next  http.RoundTripper
	calls atomic.Int32
}

func (w *wrappingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w.calls.Add(1)
	return w.next.RoundTrip(r)
}

// This test replaces a package-level variable, so it must not run in parallel.
func TestDefaultClientRespectsAReplacedDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	wrapper := &wrappingTransport{next: original}
	http.DefaultTransport = wrapper
	t.Cleanup(func() { http.DefaultTransport = original })

	server := newCountingServer(t)

	// DefaultTransport is not an *http.Transport now, so it cannot be cloned.
	// The client must neither panic nor bypass the instrumentation.
	client, err := New(WithAPIKey("k"), WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListModels(t.Context()); err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if wrapper.calls.Load() == 0 {
		t.Error("the call bypassed the replaced DefaultTransport")
	}
}
