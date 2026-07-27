module github.com/apdsoftware/postqron/features/f34-prelaunch/catalog-fixture

go 1.26.0

require github.com/apdsoftware/postqron/features/f10-entitlements v0.0.0

require (
	github.com/apdsoftware/postqron/packages/runtime v0.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.9.2 // indirect
	golang.org/x/text v0.29.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/apdsoftware/postqron/features/f10-entitlements => ../../../f10-entitlements

replace github.com/apdsoftware/postqron/packages/runtime => ../../../../packages/runtime/go
