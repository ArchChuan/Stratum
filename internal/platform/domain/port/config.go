package port

import "github.com/byteBuilderX/stratum/internal/platform/domain"

type ConfigLoader interface {
	Load() (envs []string, env domain.AppEnv, err error)
}
