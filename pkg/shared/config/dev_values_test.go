package config_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cto-up/core-lib/pkg/shared/config"
)

var devKeys = map[string]string{
	"SECRET_ENCRYPTION_KEY":      "ZGV2LW9ubHktbm90LXNlY3JldC1rZXktMzItYnl0ZXM=", // base64 of dev-only-not-secret-key-32-bytes
	"AGENT_ARCHITECT_KEY_PEPPER": "dev-only-not-secret-architect-key-pepper",
	"LIVEKIT_API_SECRET":         "secret",
}

func lookupIn(m map[string]string) func(string) string {
	return func(n string) string { return m[n] }
}

func names(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	return out
}

func TestRefuseDevKeysAcceptsDevKeysInDev(t *testing.T) {
	assert.NoError(t, config.RefuseDevKeys("dev", lookupIn(devKeys), names(devKeys)...))
}

func TestRefuseDevKeysRefusesEachDevKeyOutsideDev(t *testing.T) {
	for name, value := range devKeys {
		err := config.RefuseDevKeys("prod", lookupIn(map[string]string{name: value}), name)
		var dk config.DevKeyError
		if assert.True(t, errors.As(err, &dk), name) {
			assert.Equal(t, "refusing a dev key outside APP_ENV=dev: "+name, err.Error())
		}
	}
}

func TestRefuseDevKeysAcceptsRealKeysOutsideDev(t *testing.T) {
	real := map[string]string{
		"SECRET_ENCRYPTION_KEY":      "q3Jx1d7m8Tq0yH2vY5n9Wc4bZ6aK1sE3fR8uP0oL2iM=",
		"AGENT_ARCHITECT_KEY_PEPPER": "9f2c4e7a1b3d5f60718293a4b5c6d7e8f9012345",
		"LIVEKIT_API_SECRET":         "a-real-livekit-secret",
		"VOICE_GRANT_KEY":            "",
	}
	assert.NoError(t, config.RefuseDevKeys("", lookupIn(real), names(real)...))
}
