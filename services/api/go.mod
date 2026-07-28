module github.com/apdsoftware/postqron/services/api

go 1.26.0

require (
	github.com/apdsoftware/postqron/features/f03-auth v0.0.0
	github.com/apdsoftware/postqron/features/f04-workspaces v0.0.0
	github.com/apdsoftware/postqron/features/f10-entitlements v0.0.0
	github.com/apdsoftware/postqron/features/f12-account-privacy v0.0.0
	github.com/apdsoftware/postqron/features/f14-email v0.0.0
	github.com/apdsoftware/postqron/features/f30-app-shell v0.0.0
	github.com/apdsoftware/postqron/packages/runtime v0.0.0
	github.com/jackc/pgx/v5 v5.9.2
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/apdsoftware/postqron/packages/runtime => ../../packages/runtime/go

replace github.com/apdsoftware/postqron/features/f03-auth => ../../features/f03-auth

replace github.com/apdsoftware/postqron/features/f04-workspaces => ../../features/f04-workspaces

replace github.com/apdsoftware/postqron/features/f10-entitlements => ../../features/f10-entitlements

replace github.com/apdsoftware/postqron/features/f12-account-privacy => ../../features/f12-account-privacy

replace github.com/apdsoftware/postqron/features/f14-email => ../../features/f14-email

replace github.com/apdsoftware/postqron/features/f30-app-shell => ../../features/f30-app-shell
