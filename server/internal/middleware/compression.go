package middleware

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// gzipWriterPool reuses the compressor state across requests. A gzip.Writer
// carries a 32KB window plus its Huffman tables, so allocating one per response
// is the kind of per-request garbage that shows up as GC pressure long before it
// shows up as latency.
var gzipWriterPool = sync.Pool{
	New: func() any {
		// BestSpeed rather than the default: article JSON is highly redundant
		// (repeated keys, HTML markup) and already compresses ~5-8x at level 1.
		// The extra CPU of the default level buys single-digit percent on this
		// payload shape and is paid on every request.
		w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		return w
	},
}

// compressibleTypes are the media types worth compressing. Everything else,
// JPEG and PNG from the media library above all, is already compressed, and
// running it through gzip spends CPU to make the response marginally larger.
var compressibleTypes = []string{
	"application/json",
	"application/javascript",
	"application/xml",
	"image/svg+xml",
	"text/",
}

func compressible(contentType string) bool {
	// Content-Type carries parameters ("application/json; charset=utf-8"), so
	// match on the prefix before them.
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	for _, candidate := range compressibleTypes {
		if strings.HasPrefix(mediaType, candidate) {
			return true
		}
	}
	return false
}

// minCompressSize is the payload below which gzip is not worth it. The gzip
// header and trailer alone are 18 bytes, and a small JSON error body routinely
// comes out larger compressed than raw.
const minCompressSize = 512

// gzipResponseWriter defers the decision to compress until the first Write,
// because that is the earliest point at which Content-Type is known: handlers
// set it just before writing the body, and some let net/http sniff it.
type gzipResponseWriter struct {
	http.ResponseWriter

	gz *gzip.Writer

	// buf holds the first writes until there is either enough of the body to
	// judge it worth compressing or the handler is done. Without it a response
	// shorter than minCompressSize could not be passed through uncompressed,
	// since the headers would already be on the wire.
	buf []byte

	decided   bool
	compress  bool
	status    int
	wroteHead bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHead {
		return
	}
	g.status = code
	// Deliberately not forwarded yet: the choice to compress rewrites headers,
	// and that has to happen before the status line goes out. flush() sends it.
	g.wroteHead = true
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHead {
		g.WriteHeader(http.StatusOK)
	}
	if g.decided {
		if g.compress {
			return g.gz.Write(b)
		}
		return g.ResponseWriter.Write(b)
	}

	g.buf = append(g.buf, b...)
	if len(g.buf) < minCompressSize {
		// Not enough body yet to know which way this goes.
		return len(b), nil
	}
	if err := g.decide(true); err != nil {
		return 0, err
	}
	return len(b), nil
}

// decide commits to compressing or not, emits the headers and drains the
// buffer. large reports whether the body has already exceeded minCompressSize;
// a short body that ends at flush time is never compressed.
func (g *gzipResponseWriter) decide(large bool) error {
	g.decided = true

	header := g.Header()
	// A handler that encoded the body itself (or explicitly opted out with an
	// identity encoding) owns the wire format; never double-encode it.
	alreadyEncoded := header.Get("Content-Encoding") != ""
	g.compress = large && !alreadyEncoded && compressible(header.Get("Content-Type"))

	if g.compress {
		header.Set("Content-Encoding", "gzip")
		// Content-Length describes the uncompressed body and is now wrong.
		// Leaving it makes the client truncate the response at that many bytes.
		header.Del("Content-Length")
		g.ResponseWriter.WriteHeader(g.status)

		g.gz = gzipWriterPool.Get().(*gzip.Writer)
		g.gz.Reset(g.ResponseWriter)
		_, err := g.gz.Write(g.buf)
		g.buf = nil
		return err
	}

	g.ResponseWriter.WriteHeader(g.status)
	if len(g.buf) > 0 {
		_, err := g.ResponseWriter.Write(g.buf)
		g.buf = nil
		return err
	}
	return nil
}

// flush ends the response: it settles any still-undecided short body and closes
// the compressor so the gzip trailer is written.
func (g *gzipResponseWriter) flush() {
	if !g.decided {
		if !g.wroteHead {
			// A handler that wrote nothing at all: 204s, and the HEAD-like
			// paths. Fall through to the standard 200 net/http would send.
			g.status = http.StatusOK
		}
		_ = g.decide(false)
	}
	if g.gz != nil {
		_ = g.gz.Close()
		gzipWriterPool.Put(g.gz)
		g.gz = nil
	}
}

// Flush forwards explicit flushes, so a streaming handler is not silently
// buffered forever by this middleware.
func (g *gzipResponseWriter) Flush() {
	if !g.decided {
		// A handler that flushes is streaming: it may never reach
		// minCompressSize, and holding its bytes back would stall the client.
		_ = g.decide(len(g.buf) >= minCompressSize)
	}
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Compression gzips responses for clients that accept it.
//
// Article JSON is the reason this exists: a listing page is tens of kilobytes of
// highly redundant text, and the CMS previously shipped all of it raw, both to
// the editor SPA and through Cloudflare to the public site.
//
// Placement matters. This must sit *outside* Recovery, so that the 500 body
// Recovery writes after a panic goes through the same encoder as everything
// else. Inside it, the gzip stream would be closed during unwinding and the
// plain-text error would be appended to a finished stream, producing a response
// no client can decode. The consequence is that Logging and Metrics, which are
// outside this, keep counting uncompressed bytes; that is the same number they
// reported before compression existed, so the dashboards stay comparable.
func Compression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vary goes on unconditionally, including for clients that did not ask
		// for gzip: caches keyed on the URL alone would otherwise serve a
		// compressed body to a client that cannot read it.
		w.Header().Add("Vary", "Accept-Encoding")

		if !strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gw := &gzipResponseWriter{ResponseWriter: w, status: http.StatusOK}
		defer gw.flush()
		next.ServeHTTP(gw, r)
	})
}
