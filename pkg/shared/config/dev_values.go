package config

import (
	"encoding/base64"
	"strings"
)

// DevValuePrefix starts, raw or base64-decoded, every key committed for local
// development (office .env.dev). Such a value is public, so it is refused
// outside APP_ENV=dev.
const DevValuePrefix = "dev-only-not-secret"

// DevSeedPassword is the published local-dev admin password.
const DevSeedPassword = "Password123"

// devDefaults are published dev values that cannot carry DevValuePrefix
// because a third party fixes them: LiveKit's `--dev` secret, the README login.
var devDefaults = map[string]string{
	"LIVEKIT_API_SECRET": "secret",
	"SEED_USER_PASSWORD": DevSeedPassword,
}

// IsDevValue reports whether value, held by the variable name, is a published
// local-dev value.
func IsDevValue(name, value string) bool {
	if value == "" {
		return false
	}
	if d, ok := devDefaults[name]; ok && value == d {
		return true
	}
	if strings.HasPrefix(value, DevValuePrefix) {
		return true
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	return err == nil && strings.HasPrefix(string(decoded), DevValuePrefix)
}

// DevKeyError names the variable holding a dev value outside APP_ENV=dev.
type DevKeyError struct{ Name string }

func (e DevKeyError) Error() string {
	return "refusing a dev key outside APP_ENV=dev: " + e.Name
}

// RefuseDevKeys returns a DevKeyError for the first of names whose value, read
// through lookup, is a dev value, unless appEnv is "dev".
func RefuseDevKeys(appEnv string, lookup func(string) string, names ...string) error {
	if appEnv == "dev" {
		return nil
	}
	for _, n := range names {
		if IsDevValue(n, lookup(n)) {
			return DevKeyError{Name: n}
		}
	}
	return nil
}
