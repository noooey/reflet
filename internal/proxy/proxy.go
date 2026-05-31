package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kyuyeonpark/reflet/internal/storage"
)

var refPattern = regexp.MustCompile(`ref://[A-Za-z0-9._/-]+`)

type Server struct {
	addr      string
	store     *storage.Store
	transport *http.Transport
	ca        *certAuthority
	logger    *log.Logger
}

func New(addr string, store *storage.Store) (*Server, error) {
	cfg, err := store.Config()
	if err != nil {
		return nil, err
	}
	ca, err := newCertAuthority(cfg)
	if err != nil {
		return nil, err
	}

	return &Server{
		addr:  addr,
		store: store,
		transport: &http.Transport{
			Proxy:                 nil,
			ForceAttemptHTTP2:     false,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
		ca:     ca,
		logger: log.New(os.Stdout, "reflet-proxy ", log.LstdFlags),
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	server := &http.Server{
		Addr:              s.addr,
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		errCh <- server.Shutdown(shutdownCtx)
	}()

	s.logger.Printf("listening on http://%s", s.addr)
	err := server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	select {
	case shutdownErr := <-errCh:
		if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
			return shutdownErr
		}
	default:
	}

	return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodConnect {
		s.handleConnect(w, req)
		return
	}

	if req.URL.Path == "/__reflet/health" && (req.Host == "" || strings.HasPrefix(req.Host, s.addr)) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok\n")
		return
	}

	s.handleHTTP(w, req)
}

func (s *Server) handleHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL == nil || req.URL.Scheme == "" || req.URL.Host == "" {
		http.Error(w, "proxy requires absolute URL", http.StatusBadRequest)
		return
	}

	outReq := req.Clone(req.Context())
	outReq.RequestURI = ""
	outReq.Header = cloneHeader(req.Header)
	outReq.Header.Del("Proxy-Connection")

	substitutions, err := s.rewriteAuthorization(outReq.Header)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	s.logger.Printf("http %s %s substitutions=%d", outReq.Method, outReq.URL.String(), substitutions)

	resp, err := s.transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("proxy request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) handleConnect(w http.ResponseWriter, req *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	_, _ = io.WriteString(clientConn, "HTTP/1.1 200 Connection Established\r\n\r\n")

	go func() {
		defer clientConn.Close()
		if err := s.serveTLSStream(clientConn, req.Host); err != nil && !errors.Is(err, io.EOF) {
			s.logger.Printf("connect %s failed: %v", req.Host, err)
		}
	}()
}

func (s *Server) serveTLSStream(rawConn net.Conn, host string) error {
	cert, err := s.ca.CertificateForHost(host)
	if err != nil {
		return err
	}

	tlsConn := tls.Server(rawConn, &tls.Config{
		Certificates: []tls.Certificate{*cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err := tlsConn.Handshake(); err != nil {
		return fmt.Errorf("tls handshake: %w", err)
	}
	defer tlsConn.Close()

	br := bufio.NewReader(tlsConn)
	bw := bufio.NewWriter(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		targetURL := req.URL
		targetURL.Scheme = "https"
		targetURL.Host = host
		req.URL = targetURL
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")

		substitutions, err := s.rewriteAuthorization(req.Header)
		if err != nil {
			return err
		}

		s.logger.Printf("https %s %s substitutions=%d", req.Method, req.URL.String(), substitutions)

		resp, err := s.transport.RoundTrip(req)
		if err != nil {
			return err
		}
		if err := resp.Write(bw); err != nil {
			resp.Body.Close()
			return err
		}
		if err := bw.Flush(); err != nil {
			resp.Body.Close()
			return err
		}
		resp.Body.Close()

		if req.Close {
			return nil
		}
	}
}

func (s *Server) rewriteAuthorization(header http.Header) (int64, error) {
	values := header.Values("Authorization")
	if len(values) == 0 {
		return 0, nil
	}

	var substitutions int64
	rewritten := make([]string, 0, len(values))
	for _, value := range values {
		next, count, err := s.replaceRefs(value)
		if err != nil {
			return substitutions, err
		}
		atomic.AddInt64(&substitutions, int64(count))
		rewritten = append(rewritten, next)
	}
	header.Del("Authorization")
	for _, value := range rewritten {
		header.Add("Authorization", value)
	}
	return substitutions, nil
}

func (s *Server) replaceRefs(value string) (string, int, error) {
	var count int
	var resolveErr error
	rewritten := refPattern.ReplaceAllStringFunc(value, func(ref string) string {
		secret, err := s.store.ResolveRef(ref)
		if err != nil {
			resolveErr = err
			return ref
		}
		count++
		return secret
	})
	if resolveErr != nil {
		return value, count, resolveErr
	}
	return rewritten, count, nil
}

func cloneHeader(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
	return dst
}

func copyHeader(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
