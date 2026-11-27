package seedservice

import (
	"errors"
	"testing"

	"github.com/cto-up/core-lib/pkg/shared/config"
)

func TestSeedRefusesTheDevPasswordOutsideDev(t *testing.T) {
	t.Cleanup(func() { config.Set(config.Config{}) })
	for _, appEnv := range []string{"", "prod", "preprod"} {
		config.Set(config.Config{Seed: config.Seed{
			UserEmail: "admin@ctoup.localhost", UserPassword: DevSeedPassword, AppEnv: appEnv,
		}})
		// No store and no auth client: the refusal comes before either is touched.
		err := (&SeedService{}).Seed()
		if !errors.Is(err, ErrDevSeedPassword) {
			t.Errorf("APP_ENV=%q: Seed() = %v, want ErrDevSeedPassword", appEnv, err)
		}
	}
}

func TestCheckSeedPasswordAllowsDevOrAnotherPassword(t *testing.T) {
	cases := []config.Seed{
		{UserPassword: DevSeedPassword, AppEnv: "dev"},
		{UserPassword: "a-real-password", AppEnv: ""},
		{UserPassword: "a-real-password", AppEnv: "prod"},
	}
	for _, s := range cases {
		if err := checkSeedPassword(s); err != nil {
			t.Errorf("checkSeedPassword(%+v) = %v, want nil", s, err)
		}
	}
}
