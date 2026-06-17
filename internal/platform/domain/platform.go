// Package domain holds cross-cutting platform entities (config / harness
// surface). Phase 4 may keep this minimal — most platform concerns are
// infrastructure rather than domain.
package domain

type AppEnv string

const (
	EnvDev     AppEnv = "dev"
	EnvStaging AppEnv = "staging"
	EnvProd    AppEnv = "production"
)
