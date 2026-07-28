module github.com/apdsoftware/postqron/services/worker

go 1.26.0

require (
	github.com/apdsoftware/postqron/features/f04-workspaces v0.0.0
	github.com/apdsoftware/postqron/features/f14-email v0.0.0
	github.com/apdsoftware/postqron/packages/runtime v0.0.0
)

require (
	github.com/jackc/pgx/v5 v5.9.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/apdsoftware/postqron/packages/runtime => ../../packages/runtime/go

replace github.com/apdsoftware/postqron/features/f04-workspaces => ../../features/f04-workspaces

replace github.com/apdsoftware/postqron/features/f14-email => ../../features/f14-email
