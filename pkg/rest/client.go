// Copyright 2024 RackTop Systems Inc. and/or its affiliates.
// http://www.racktopsystems.com

package rest

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt"
)

const (
	VERSION string = "1.0"
	AGENT   string = "BsrCSI"
)

type BsrClientOpts struct {
	ConnectionTimeout  time.Duration
	KeepAlive          time.Duration
	IdleConnTimeout    time.Duration
	InsecureSkipVerify bool
}

type BsrClient struct {
	client *http.Client

	tlsconfig *tls.Config

	auth   *AuthHeader
	token  string
	claims *BsrClaims

	lastLogin time.Time

	// Protects auth, token, claims and lastLogin.
	// The mutex is held during http request setup for token expiration checks.
	// If the token is empty or expired, the mutex is held during the network
	// login round trip to get a new token.
	mutex sync.Mutex

	hostAndPort string

	BaseURL string

	LoginUrl string
}

func newTransport(opts BsrClientOpts) *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   opts.ConnectionTimeout,
			KeepAlive: opts.KeepAlive,
		}).DialContext,
		TLSHandshakeTimeout: opts.ConnectionTimeout,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: opts.InsecureSkipVerify,
		},
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     false,
	}
}

// NewBsrClient creates a new client using the specified auth mechanism.
func NewBsrClient(baseurl string, opts BsrClientOpts, auth *AuthHeader) *BsrClient {

	// set defaults for transport if not specified
	if opts.ConnectionTimeout == 0 {
		opts.ConnectionTimeout = 5 * time.Second
	}
	if opts.KeepAlive == 0 {
		opts.KeepAlive = 30 * time.Second
	}
	if opts.IdleConnTimeout == 0 {
		opts.IdleConnTimeout = 90 * time.Second
	}

	transport := newTransport(opts)
	transport.MaxIdleConns = 100
	transport.IdleConnTimeout = opts.IdleConnTimeout

	// ToDo: add cert trusts through additional options

	b := &BsrClient{
		tlsconfig: transport.TLSClientConfig,
		client:    &http.Client{Transport: transport},
	}

	b.hostAndPort = strings.TrimPrefix(baseurl, "https://")

	b.BaseURL = baseurl

	// set default http timeout
	b.client.Timeout = opts.IdleConnTimeout

	b.mutex.Lock()
	b.auth = auth
	b.token = ""
	b.claims = nil
	b.mutex.Unlock()

	return b
}

func (b *BsrClient) Host() string {
	host, _, _ := net.SplitHostPort(b.hostAndPort)
	return host
}

// HostAndPort returns host:port
func (b *BsrClient) HostAndPort() string {
	return b.hostAndPort
}

// HttpClient returns the http client
func (b *BsrClient) HttpClient() *http.Client {
	return b.client
}

// TlsConfig returns a pointer to the tls config
func (b *BsrClient) TlsConfig() *tls.Config {
	return b.tlsconfig
}

// LastLogin returns last time the client logged into the Brickstor API
func (b *BsrClient) LastLogin() time.Time {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.lastLogin
}

// HasToken returns true if the client has a jwt token already
func (b *BsrClient) HasToken() bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.token != "" && b.claims != nil && time.Now().Unix() < b.claims.ExpiresAt
}

// Login ensures a JWT token is set and not expired for making http calls.
// This happens automatically when making http calls. If the token is empty or
// expired, concurrent login and auto-login calls will block on
// a single network roundtrip to login. After that completes, subsequent http
// calls are concurrent.
func (b *BsrClient) Login() error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.login()
}

// Return the http client
func (b *BsrClient) login() error {
	// if no credentials set, assume anonymous endpoint
	if b.auth == nil {
		return nil
	}

	// if we have a token and it does not expire within 1 minute, then just exit
	if b.token != "" &&
		b.claims != nil &&
		time.Now().Add(60*time.Second).Unix() < b.claims.ExpiresAt {
		return nil
	}

	b.token = ""
	b.claims = nil

	url := b.LoginUrl
	if url == "" {
		url = b.BaseURL + "/login"
	}

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return fmt.Errorf("error creating request: %s", err)
	}

	// set headers
	b.setAuthHeaders(req)

	response, err := b.do(b.client, req)
	if err != nil {
		return err
	}

	// always read and close the body to facilitate http2 connection resuse
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()

	if err != nil {
		return fmt.Errorf("error reading response: %s", err)
	}

	// If we got back anything other than a 200, we failed login
	switch response.StatusCode {
	case 401:
		if b.auth.authtype == BEARER {
			return errors.New("invalid or expired token")
		} else {
			return errors.New("invalid credentials")
		}
	case 200:
		// ok
	default:
		return errors.New("internal server error")
	}

	// deserialize token
	m := make(map[string]string)
	err = json.Unmarshal(body, &m)
	if err != nil {
		return fmt.Errorf("invalid response: %s", err)
	}

	token := m["token"]
	if token == "" {
		return fmt.Errorf("invalid response: missing token")
	}

	parsedToken, err := jwt.ParseWithClaims(token, &BsrClaims{},
		func(token *jwt.Token) (interface{}, error) {
			// dummy function - we don't care about validating here for now. We
			// just want to parse. Eventually want to only accept
			return []byte(""), nil
		})

	if err != nil && err.Error() != jwt.ErrInvalidKeyType.Error() {
		return fmt.Errorf("error parsing token: %s", err)
	}

	b.token = token
	if parsedToken != nil {
		b.claims = parsedToken.Claims.(*BsrClaims)
	}

	if b.auth.authtype == BEARER {
		b.auth.token = token
	}

	b.lastLogin = time.Now()

	return nil
}

// newToken discards the existing auth token then creates a new one
func (b *BsrClient) newToken() error {

	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.token = ""

	return b.login()
}

// Post posts the request as json and decodes the json response.
func (b *BsrClient) Post(relPath string, request, response interface{}) error {

	resp, respErr := b.postJson(relPath, request)

	decodeErr := decodeResponse(resp, respErr, response)

	// Check for an authentication error. Instead of waiting for the jwt to
	// expire we create a new one and retry the POST.
	if decodeErr != nil && resp != nil && resp.StatusCode == http.StatusUnauthorized {
		// Discard current token and create a new one
		newTokenErr := b.newToken()
		if newTokenErr != nil {
			return fmt.Errorf("error generating new auth token: %w", newTokenErr)
		}

		// Retry transaction using the new token
		resp, respErr = b.postJson(relPath, request)
		decodeErr = decodeResponse(resp, respErr, response)
	}

	return decodeErr
}

func (b *BsrClient) postJson(relPath string, request interface{}) (*http.Response, error) {
	buf := new(bytes.Buffer)

	err := json.NewEncoder(buf).Encode(request)

	if err != nil {
		return nil, fmt.Errorf("error encoding request: %s", err)
	}

	req, err := b.createRequest("POST", relPath, buf)
	if err != nil {
		return nil, err
	}

	return b.do(b.client, req)
}

// Get sends the request as json and decodes the json response.
func (b *BsrClient) Get(relPath string, request, response interface{}) error {

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(request); err != nil {
		return fmt.Errorf("error encoding request: %s", err)
	}

	req, err := b.createRequest("GET", relPath, buf)
	if err != nil {
		return err
	}

	resp, err := b.do(b.client, req)

	return decodeResponse(resp, err, response)
}

// Helper to create an http request
func (b *BsrClient) createRequest(command string, relPath string, body io.Reader) (*http.Request, error) {

	b.mutex.Lock()
	defer b.mutex.Unlock()

	// clean path
	relPath = strings.TrimPrefix(relPath, b.BaseURL)

	// make sure we have proper credentials or a valid token
	err := b.login()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(command, b.BaseURL+relPath, body)
	if err != nil {
		return nil, err
	}

	b.setAuthHeaders(req)

	return req, nil
}

func (b *BsrClient) setAuthHeaders(req *http.Request) {
	// set authorization header
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	} else if b.auth != nil {
		switch b.auth.authtype {
		case BASIC:
			req.SetBasicAuth(b.auth.user, b.auth.pass)
		case BEARER:
			req.Header.Set("Authorization", "Bearer "+b.auth.token)
		case SSL:
			req.SetBasicAuth(b.auth.user, "")
			b.tlsconfig.Certificates = []tls.Certificate{b.auth.ssl}
		}
	}

	req.Header.Set("Content-Type", "application/json")

	req.Header.Set("Cache-Control", "no-cache")

	req.Header.Set("User-Agent", UserAgent())

	req.Header.Set("X-BsrCli-Version", VERSION)

	// Tell the server the maximum duration you are willing to wait for a
	// response. Both the client and the server should take into consideration
	// network latencies when using this header.
	dur := JsonDuration{Duration: b.client.Timeout}
	req.Header.Set("X-Request-Timeout", dur.String())
}

func decodeResponse(resp *http.Response, err error, v interface{}) error {
	if err != nil {
		return err
	}

	// always read and close the body to facilitate http2 connection resuse
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if err != nil {
		return fmt.Errorf("error reading response: %s", err)
	}

	// if we get an http error, then we need to parse the output
	if resp.StatusCode != 200 {
		errResp := &ErrorResponse{}
		err = json.Unmarshal(body, errResp)
		if err != nil {
			return fmt.Errorf("error decoding error response: %s", err)
		}
		return errResp
	}

	if v != nil {
		err = json.Unmarshal(body, v)
		if err != nil {
			return fmt.Errorf("error decoding response: %s", err)
		}
	}

	return nil
}

// do wraps the http client Do(req) to enable metrics collection and callbacks to run
func (b *BsrClient) do(c *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := c.Do(req)

	return resp, err
}
