package config_test

import (
	"testing"

	"github.com/cto-up/core-lib/pkg/shared/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrustedProxiesDefaultToLoopbackAndThePrivateRanges(t *testing.T) {
	got, err := config.ParseTrustedProxies("  ")
	require.NoError(t, err)
	assert.Equal(t, config.DefaultTrustedProxies, got)
	assert.NotContains(t, got, "100.64.0.0/10", "the tailnet is not a proxy")
}

func TestASetTrustedProxiesReplacesTheDefault(t *testing.T) {
	got, err := config.ParseTrustedProxies("172.18.0.1, 10.10.0.0/24")
	require.NoError(t, err)
	assert.Equal(t, []string{"172.18.0.1", "10.10.0.0/24"}, got)
}

func TestAnInvalidTrustedProxyIsAnError(t *testing.T) {
	_, err := config.ParseTrustedProxies("10.10.0.0/24,10.10.0.300")
	require.ErrorContains(t, err, `"10.10.0.300"`)
	_, err = config.ParseTrustedProxies(" , ")
	require.Error(t, err)
}

func TestFromEnvReadsTrustedProxies(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "172.18.0.1")
	assert.Equal(t, "172.18.0.1", config.FromEnv().Network.TrustedProxies)
}
