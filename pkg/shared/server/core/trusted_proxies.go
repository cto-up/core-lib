package core

import "github.com/gin-gonic/gin"

// TrustClientIPFrom makes c.ClientIP() the peer's own address, unless the peer
// is one of the trusted proxies, in which case it is the X-Real-IP that proxy
// set. X-Forwarded-For is never read: nginx appends to whatever the client
// sent, so its left end is the client's choice. A trusted peer with no valid
// X-Real-IP gives the peer's own address (gin's fallback).
func TrustClientIPFrom(router *gin.Engine, trusted []string) error {
	router.ForwardedByClientIP = true
	router.RemoteIPHeaders = []string{"X-Real-IP"}
	return router.SetTrustedProxies(trusted)
}
