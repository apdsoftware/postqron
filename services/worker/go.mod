module github.com/apdsoftware/postqron/services/worker

go 1.26.0

require (
	github.com/apdsoftware/postqron/features/f03-auth v0.0.0
	github.com/apdsoftware/postqron/features/f04-workspaces v0.0.0
	github.com/apdsoftware/postqron/features/f05-social-connections v0.0.0
	github.com/apdsoftware/postqron/features/f08-publishing v0.0.0
	github.com/apdsoftware/postqron/features/f14-email v0.0.0
	github.com/apdsoftware/postqron/packages/runtime v0.0.0
	github.com/jackc/pgx/v5 v5.9.2
)

require (
	github.com/coreos/go-oidc/v3 v3.17.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	golang.org/x/crypto v0.41.0 // indirect
	golang.org/x/oauth2 v0.31.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/apdsoftware/postqron/packages/runtime => ../../packages/runtime/go

replace github.com/apdsoftware/postqron/features/f03-auth => ../../features/f03-auth

replace github.com/apdsoftware/postqron/features/f04-workspaces => ../../features/f04-workspaces

replace github.com/apdsoftware/postqron/features/f05-social-connections => ../../features/f05-social-connections

replace github.com/apdsoftware/postqron/features/f08-publishing => ../../features/f08-publishing

replace github.com/apdsoftware/postqron/features/f14-email => ../../features/f14-email
