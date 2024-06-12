// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package testutils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"
)

// HTTPProxy creates an HTTP/HTTPS/CONNECT proxy service you can use to test proxy behavior.
func HTTPProxy(t *testing.T, options ...HTTPProxyOption) HTTPProxyService {
	ca := CA(t)

	keyPair := ca.CreateLocalhostServerCert()

	opts := httpProxyOptions{}
	for _, opt := range options {
		if err := opt(&opts); err != nil {
			t.Fatalf("❌ Failed to initalize HTTP proxy service (%v)", err)
		}
	}

	service := &httpProxyService{
		t:            t,
		ca:           ca,
		keyPair:      keyPair,
		proxyOptions: opts,
		mutex:        &sync.Mutex{},
	}
	if err := service.start(); err != nil {
		t.Fatalf("❌ Failed to initalize HTTP proxy service (%v)", err)
	}
	t.Cleanup(func() {
		service.stop()
	})

	return service
}

// HTTPProxyService is an HTTP/HTTPS/CONNECT proxy service for testing purposes.
type HTTPProxyService interface {
	// HTTPProxy returns the HTTP proxy address.
	HTTPProxy() *url.URL
	// HTTPSProxy returns the HTTPS proxy address.
	HTTPSProxy() *url.URL
	// CACert returns the CA certificate in PEM format for the HTTPSProxy address.
	CACert() []byte
}

// HTTPProxyOptionForceHTTPTarget forces non-CONNECT (HTTP/HTTPS) requests to be sent to the specified target via an
// HTTP request regardless of the request. You should specify the target as hostname:ip.
func HTTPProxyOptionForceHTTPTarget(target string) HTTPProxyOption {
	return func(options *httpProxyOptions) error {
		options.httpTarget = target
		options.targetIsHTTPS = false
		return nil
	}
}

// HTTPProxyOptionForceHTTPSTarget forces non-CONNECT (HTTP/HTTPS) requests to be sent to the specified target via an
// HTTPS request. If the backing server is using a custom CA, you should pass the caCert as the second parameter.
func HTTPProxyOptionForceHTTPSTarget(target string, caCert []byte) HTTPProxyOption {
	return func(options *httpProxyOptions) error {
		options.httpTarget = target
		options.targetIsHTTPS = true
		options.targetCACert = caCert
		return nil
	}
}

// HTTPProxyOptionForceCONNECTTarget forces CONNECT requests to be sent to the specified target, regardless
// of the request.
func HTTPProxyOptionForceCONNECTTarget(target string) HTTPProxyOption {
	return func(options *httpProxyOptions) error {
		options.connectTarget = target
		return nil
	}
}

// HTTPProxyOption is a function that changes the settings for the proxy server. The parameter is intentionally not
// exposed.
type HTTPProxyOption func(options *httpProxyOptions) error

type httpProxyOptions struct {
	httpTarget    string
	targetIsHTTPS bool
	targetCACert  []byte
	connectTarget string
}

type httpProxyService struct {
	t            *testing.T
	ca           CertificateAuthority
	keyPair      KeyPair
	proxyOptions httpProxyOptions
	mutex        *sync.Mutex

	httpListener  net.Listener
	httpsListener net.Listener

	httpServer  *http.Server
	httpsServer *http.Server

	httpAddr  *net.TCPAddr
	httpsAddr *net.TCPAddr

	httpErr  error
	httpsErr error
}

func (h *httpProxyService) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodConnect {
		h.handleConnect(writer, request)
	} else {
		h.handleHTTP(writer, request)
	}
}

func (h *httpProxyService) handleHTTP(writer http.ResponseWriter, request *http.Request) {
	requestURI := request.RequestURI

	requestURLParsed, err := url.Parse(requestURI)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	request.RequestURI = ""
	if h.proxyOptions.targetIsHTTPS {
		requestURLParsed.Scheme = "https"
	} else {
		requestURLParsed.Scheme = "http"
	}
	request.URL = requestURLParsed
	request.Header.Del("Proxy-Authorization")

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if len(h.proxyOptions.targetCACert) > 0 {
		certPool := x509.NewCertPool()
		certPool.AppendCertsFromPEM(h.proxyOptions.targetCACert)
		tlsConfig.RootCAs = certPool
	}

	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}
	if h.proxyOptions.httpTarget != "" {
		httpClient.Transport.(*http.Transport).DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", h.proxyOptions.httpTarget)
		}
		httpClient.Transport.(*http.Transport).DialTLSContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&tls.Dialer{
				NetDialer: &net.Dialer{},
				Config:    tlsConfig,
			}).DialContext(ctx, "tcp", h.proxyOptions.httpTarget)
		}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		writer.WriteHeader(http.StatusBadGateway)
		return
	}
	defer func() {
		_ = response.Body.Close()
	}()
	writer.WriteHeader(response.StatusCode)
	for header, value := range response.Header {
		for _, v := range value {
			writer.Header().Add(v, header)
		}
	}
	_, _ = io.Copy(writer, response.Body)
}

func (h *httpProxyService) handleConnect(writer http.ResponseWriter, request *http.Request) {
	requestURI := request.RequestURI
	if h.proxyOptions.connectTarget != "" {
		requestURI = h.proxyOptions.connectTarget
	}
	serverConn, err := net.Dial("tcp", requestURI)
	if err != nil {
		writer.WriteHeader(http.StatusBadGateway)
		return
	}
	defer func() {
		_ = serverConn.Close()
	}()
	hijack, ok := writer.(http.Hijacker)
	if !ok {
		writer.WriteHeader(http.StatusBadGateway)
		return
	}

	clientConn, buf, err := hijack.Hijack()
	if err != nil {
		writer.WriteHeader(http.StatusBadGateway)
		return
	}
	defer func() {
		_ = clientConn.Close()
	}()

	wg := &sync.WaitGroup{}
	wg.Add(2) //nolint:mnd // This is stupid.
	go func() {
		_, _ = io.Copy(buf, serverConn)
	}()
	go func() {
		_, _ = io.Copy(serverConn, clientConn)
	}()
	wg.Wait()
}

func (h *httpProxyService) HTTPProxy() *url.URL {
	//goland:noinspection HttpUrlsUsage
	u, err := url.Parse("http://" + h.httpAddr.IP.String() + ":" + strconv.Itoa(h.httpAddr.Port))
	if err != nil {
		panic(err)
	}
	return u
}

func (h *httpProxyService) HTTPSProxy() *url.URL {
	u, err := url.Parse("https://" + h.httpsAddr.IP.String() + ":" + strconv.Itoa(h.httpsAddr.Port))
	if err != nil {
		panic(err)
	}
	return u
}

func (h *httpProxyService) CACert() []byte {
	return h.ca.GetPEMCACert()
}

func (h *httpProxyService) start() error {
	h.t.Logf("🚀 Starting HTTP proxy service...")

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{
			h.keyPair.GetTLSCertificate(),
		},
		MinVersion: tls.VersionTLS13,
	}

	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to listen on HTTP port (%w)", err)
	}
	httpsListener, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to listen on HTTPS port (%w)", err)
	}

	h.httpAddr = httpListener.Addr().(*net.TCPAddr)   //nolint:errcheck //This is always a TCPAddr, see above.
	h.httpsAddr = httpsListener.Addr().(*net.TCPAddr) //nolint:errcheck //This is always a TCPAddr, see above.

	h.httpServer = &http.Server{
		Addr:      h.httpAddr.IP.String() + ":" + strconv.Itoa(h.httpAddr.Port),
		Handler:   h,
		TLSConfig: nil,
		ErrorLog:  NewGoTestLogger(h.t),
		BaseContext: func(_ net.Listener) context.Context {
			return Context(h.t)
		},
		ReadHeaderTimeout: httpHeaderReadTimeout,
	}
	h.httpsServer = &http.Server{
		Addr:      h.httpsAddr.IP.String() + ":" + strconv.Itoa(h.httpsAddr.Port),
		Handler:   h,
		TLSConfig: tlsConfig,
		ErrorLog:  NewGoTestLogger(h.t),
		BaseContext: func(_ net.Listener) context.Context {
			return Context(h.t)
		},
		ReadHeaderTimeout: httpHeaderReadTimeout,
	}
	h.httpListener = httpListener
	h.httpsListener = httpsListener

	go h.runHTTP()
	go h.runHTTPS()
	if err := h.waitForService(); err != nil {
		return err
	}

	h.t.Logf("✅ Started %s", h.String())
	return nil
}

func (h *httpProxyService) runHTTP() {
	httpErr := h.httpServer.Serve(h.httpListener)
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.httpErr = httpErr
}
func (h *httpProxyService) runHTTPS() {
	httpsErr := h.httpsServer.Serve(h.httpsListener)
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.httpsErr = httpsErr
}

func (h *httpProxyService) stop() {
	h.t.Logf("⚙️ Stopping %s", h.String())

	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	if err := h.httpServer.Shutdown(ctx); err != nil {
		if !errors.Is(err, http.ErrServerClosed) {
			h.t.Errorf("❌ HTTP server failed to shut down correctly: %v", err)
		}
	}
	if err := h.httpsServer.Shutdown(ctx); err != nil {
		if !errors.Is(err, http.ErrServerClosed) {
			h.t.Errorf("❌ HTTPS server failed to shut down correctly: %v", err)
		}
	}

	h.t.Logf("✅ HTTP proxy service now stopped.")
}

func (h *httpProxyService) String() string {
	if h.httpListener == nil {
		return "HTTP proxy service (stopped)"
	}
	return fmt.Sprintf("HTTP proxy service (running at %s and %s)", h.HTTPProxy(), h.HTTPSProxy())
}

func (h *httpProxyService) waitForService() error {
	h.t.Logf("⌚ Waiting for HTTP/HTTPS proxy services to become available...")
	ctx, cancel := context.WithTimeout(Context(h.t), httpProxyTimeoutUp)
	defer cancel()
	httpUp := false
	httpsUp := false
	for {
		httpClient := HTTPClientForCA(h.CACert())
		if !httpUp {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.HTTPProxy().String(), nil)
			if err != nil {
				return fmt.Errorf("cannot create HTTP request (%w)", err)
			}
			resp, err := httpClient.Do(req)
			if err == nil {
				h.t.Logf("✅ HTTP proxy service is up.")
				_ = resp.Body.Close()
				httpUp = true
			} else {
				h.t.Logf("⌚ Still waiting for the HTTP proxy service to come up...")
			}
		}
		if !httpsUp {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.HTTPSProxy().String(), nil)
			if err != nil {
				return fmt.Errorf("cannot create HTTP request (%w)", err)
			}
			resp, err := httpClient.Do(req)
			if err == nil {
				h.t.Logf("✅ HTTPS proxy service is up.")
				_ = resp.Body.Close()
				httpsUp = true
			} else {
				h.t.Logf("⌚ Still waiting for the HTTPS proxy service to come up...")
			}
		}
		if httpUp && httpsUp {
			return nil
		}
		h.mutex.Lock()
		if h.httpErr != nil {
			h.mutex.Unlock()
			return fmt.Errorf("the HTTP proxy service exited with error: %w", h.httpErr)
		}
		if h.httpsErr != nil {
			h.mutex.Unlock()
			return fmt.Errorf("the HTTPS proxy service exited with error: %w", h.httpsErr)
		}
		h.mutex.Unlock()
		select {
		case <-ctx.Done():
			if httpUp {
				return fmt.Errorf("timeout: the HTTPS service failed to come up")
			} else if httpsUp {
				return fmt.Errorf("timeout: the HTTP service failed to come up")
			} else {
				return fmt.Errorf("timeout: both the HTTP and HTTPS services failed to come up")
			}
		case <-time.After(time.Second):
		}
	}
}
