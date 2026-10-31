package service

import (
	"github.com/cto-up/core-lib/pkg/core/db"
	"github.com/cto-up/core-lib/pkg/shared/auth"
)

// UserServiceStrategyFactory creates the appropriate strategy based on provider
type UserServiceStrategyFactory struct{}

func (f *UserServiceStrategyFactory) CreateUserServiceStrategy(store *db.Store, authClientPool auth.AuthClientPool) UserService {
	return NewSharedUserService(store, authClientPool)
}

func NewUserServiceStrategyFactory() *UserServiceStrategyFactory {
	return &UserServiceStrategyFactory{}
}
