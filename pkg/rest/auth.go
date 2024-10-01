// Copyright 2024 RackTop Systems Inc. and/or its affiliates.
// http://www.racktopsystems.com

package rest

import (
	"crypto/tls"
	"fmt"

	"github.com/golang-jwt/jwt"
)

type AuthType string

const (
	BASIC  AuthType = "BASIC"
	BEARER AuthType = "BEARER"
	SSL    AuthType = "SSL"
)

// authHeader contains client authentication parameters
type AuthHeader struct {
	authtype AuthType
	user     string
	pass     string
	token    string
	ssl      tls.Certificate
}

// NewBasicAuth creates a new authHeader using a username and password
func NewBasicAuth(user string, pass string) *AuthHeader {
	return &AuthHeader{
		authtype: BASIC,
		user:     user,
		pass:     pass,
	}
}

// NewJWTAuth creates a new authHeader using a jwt token
func NewJWTAuth(token string) *AuthHeader {
	return &AuthHeader{
		authtype: BEARER,
		token:    token,
	}
}

// NewSslAuth creates a new authHeader using an ssl certificate
func NewSslAuth(user string, cert tls.Certificate) *AuthHeader {
	return &AuthHeader{
		authtype: SSL,
		user:     user,
		ssl:      cert,
	}
}

func UserAgent() string {
	return fmt.Sprintf("%s/%s", AGENT, VERSION)
}

// RacktopClaims custom claims
type RacktopClaims struct {
	Name             string   `json:"name"`
	Groups           []string `json:"groups"`
	Version          string   `json:"ver"`
	Refresh          string   `json:"ref"`
	Validate         string   `json:"val"`
	IdentityProvider string   `json:"idp"`

	CustomerIds []string `json:"customers"`
	Serials     []string `json:"serials"`

	jwt.StandardClaims
}

// BrickstorOS custom claims
type BsrClaims struct {
	RacktopClaims

	CustomerId string
	Serial     string
}
